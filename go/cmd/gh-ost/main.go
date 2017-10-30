/*
   Copyright 2016 GitHub Inc.
	 See https://github.com/github/gh-ost/blob/master/LICENSE
*/

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/github/gh-ost/go/base"
	"github.com/github/gh-ost/go/logic"
	log "github.com/wfxiang08/cyutils/utils/rolling_log"

	"golang.org/x/crypto/ssh/terminal"
)

var AppVersion string

// acceptSignals registers for OS signals
func acceptSignals(migrationContext *base.MigrationContext) {
	c := make(chan os.Signal, 1)

	signal.Notify(c, syscall.SIGHUP)
	go func() {
		for sig := range c {
			switch sig {
			case syscall.SIGHUP:
				// kill -HUP xxx
				// 重新加载配置文件
				// 这个时候重新加载有什么意义呢?
				log.Infof("Received SIGHUP. Reloading configuration")
				if err := migrationContext.ReadConfigFile(); err != nil {
					log.ErrorErrorf(err, "migrationContext ReadConfigFile failed")
				} else {
					migrationContext.MarkPointOfInterest()
				}
			}
		}
	}()
}

// main is the application's entry point. It will either spawn a CLI or HTTP interfaces.
func main() {
	migrationContext := base.GetMigrationContext()

	dbAlias := flag.String("db_alias", "", "db alias in db conf file")
	dbConfigFile := flag.String("db_conf", "", "db config file")

	flag.StringVar(&migrationContext.InspectorConnectionConfig.Key.Hostname, "host", "127.0.0.1", "MySQL hostname (preferably a replica, not the master)")

	// 主动告知master(可以不告知的)
	flag.StringVar(&migrationContext.AssumeMasterHostname, "assume-master-host", "", "(optional) explicitly tell gh-ost the identity of the master. Format: some.host.com[:port] This is useful in master-master setups where you wish to pick an explicit master, or in a tungsten-replicator where gh-ost is unabel to determine the master")
	flag.IntVar(&migrationContext.InspectorConnectionConfig.Key.Port, "port", 3306, "MySQL port (preferably a replica, not the master)")

	flag.StringVar(&migrationContext.CliUser, "user", "", "MySQL user")
	flag.StringVar(&migrationContext.CliPassword, "password", "", "MySQL password")
	flag.StringVar(&migrationContext.CliMasterUser, "master-user", "", "MySQL user on master, if different from that on replica. Requires --assume-master-host")
	flag.StringVar(&migrationContext.CliMasterPassword, "master-password", "", "MySQL password on master, if different from that on replica. Requires --assume-master-host")

	// XXX: 配置文件的作用?
	flag.StringVar(&migrationContext.ConfigFile, "conf", "", "Config file")

	// 可以不指定密码，每次由运维来主动输入
	askPass := flag.Bool("ask-pass", false, "prompt for MySQL password")

	flag.StringVar(&migrationContext.DatabaseName, "database", "", "database name (mandatory)")
	flag.StringVar(&migrationContext.OriginalTableName, "table", "", "table name (mandatory)")
	flag.StringVar(&migrationContext.AlterStatement, "alter", "", "alter statement (mandatory)")

	// 不使用精确的Rows估计算法(execution timeout可能使得精确估计不大可能)
	// 是否使用精确的行数
	flag.BoolVar(&migrationContext.CountTableRows, "exact-rowcount", false, "actually count table rows as opposed to estimate them (results in more accurate progress estimation)")

	flag.BoolVar(&migrationContext.ConcurrentCountTableRows, "concurrent-rowcount", true, "(with --exact-rowcount), when true (default): count rows after row-copy begins, concurrently, and adjust row estimate later on; when false: first count rows, then start row copy")

	// 是否允许运行在master上呢？
	// 虽然写操作是在master上的，但是查询操作默认是在slave上的
	flag.BoolVar(&migrationContext.AllowedRunningOnMaster, "allow-on-master", false, "allow this migration to run directly on master. Preferably it would run on a replica")
	// 是否允许 master <---> master(双主）
	flag.BoolVar(&migrationContext.AllowedMasterMaster, "allow-master-master", false, "explicitly allow running in a master-master setup")
	flag.BoolVar(&migrationContext.NullableUniqueKeyAllowed, "allow-nullable-unique-key", false, "allow gh-ost to migrate based on a unique key with nullable columns. As long as no NULL values exist, this should be OK. If NULL values exist in chosen key, data may be corrupted. Use at your own risk!")
	flag.BoolVar(&migrationContext.ApproveRenamedColumns, "approve-renamed-columns", false, "in case your `ALTER` statement renames columns, gh-ost will note that and offer its interpretation of the rename. By default gh-ost does not proceed to execute. This flag approves that gh-ost's interpretation si correct")
	flag.BoolVar(&migrationContext.SkipRenamedColumns, "skip-renamed-columns", false, "in case your `ALTER` statement renames columns, gh-ost will note that and offer its interpretation of the rename. By default gh-ost does not proceed to execute. This flag tells gh-ost to skip the renamed columns, i.e. to treat what gh-ost thinks are renamed columns as unrelated columns. NOTE: you may lose column data")
	flag.BoolVar(&migrationContext.IsTungsten, "tungsten", false, "explicitly let gh-ost know that you are running on a tungsten-replication based topology (you are likely to also provide --assume-master-host)")

	// 可以默认打开，至少目前我们没有这个需求
	flag.BoolVar(&migrationContext.DiscardForeignKeys, "discard-foreign-keys", false, "DANGER! This flag will migrate a table that has foreign keys and will NOT create foreign keys on the ghost table, thus your altered table will have NO foreign keys. This is useful for intentional dropping of foreign keys")
	flag.BoolVar(&migrationContext.SkipForeignKeyChecks, "skip-foreign-key-checks", false, "set to 'true' when you know for certain there are no foreign keys on your table, and wish to skip the time it takes for gh-ost to verify that")

	executeFlag := flag.Bool("execute", false, "actually execute the alter & migrate the table. Default is noop: do some tests and exit")
	flag.BoolVar(&migrationContext.TestOnReplica, "test-on-replica", false, "Have the migration run on a replica, not on the master. At the end of migration replication is stopped, and tables are swapped and immediately swap-revert. Replication remains stopped and you can compare the two tables for building trust")
	flag.BoolVar(&migrationContext.TestOnReplicaSkipReplicaStop, "test-on-replica-skip-replica-stop", false, "When --test-on-replica is enabled, do not issue commands stop replication (requires --test-on-replica)")
	flag.BoolVar(&migrationContext.MigrateOnReplica, "migrate-on-replica", false, "Have the migration run on a replica, not on the master. This will do the full migration on the replica including cut-over (as opposed to --test-on-replica)")

	// 是否删除old table
	flag.BoolVar(&migrationContext.OkToDropTable, "ok-to-drop-table", false, "Shall the tool drop the old table at end of operation. DROPping tables can be a long locking operation, which is why I'm not doing it by default. I'm an online tool, yes?")
	flag.BoolVar(&migrationContext.InitiallyDropOldTable, "initially-drop-old-table", false, "Drop a possibly existing OLD table (remains from a previous run?) before beginning operation. Default is to panic and abort if such table exists")
	flag.BoolVar(&migrationContext.InitiallyDropGhostTable, "initially-drop-ghost-table", false, "Drop a possibly existing Ghost table (remains from a previous run?) before beginning operation. Default is to panic and abort if such table exists")

	// 表名是否带上时间戳
	flag.BoolVar(&migrationContext.TimestampOldTable, "timestamp-old-table", false, "Use a timestamp in old table name. This makes old table names unique and non conflicting cross migrations")

	// 数据拷贝完毕，如何进行近cut-over呢?
	cutOver := flag.String("cut-over", "atomic", "choose cut-over type (default|atomic, two-step)")
	flag.BoolVar(&migrationContext.ForceNamedCutOverCommand, "force-named-cut-over", false, "When true, the 'unpostpone|cut-over' interactive command must name the migrated table")

	flag.BoolVar(&migrationContext.SwitchToRowBinlogFormat, "switch-to-rbr", false, "let this tool automatically switch binary log format to 'ROW' on the replica, if needed. The format will NOT be switched back. I'm too scared to do that, and wish to protect you if you happen to execute another migration while this one is running")
	flag.BoolVar(&migrationContext.AssumeRBR, "assume-rbr", false, "set to 'true' when you know for certain your server uses 'ROW' binlog_format. gh-ost is unable to tell, event after reading binlog_format, whether the replication process does indeed use 'ROW', and restarts replication to be certain RBR setting is applied. Such operation requires SUPER privileges which you might not have. Setting this flag avoids restarting replication and you can proceed to use gh-ost without SUPER privileges")
	chunkSize := flag.Int64("chunk-size", 1000, "amount of rows to handle in each iteration (allowed range: 100-100,000)")
	dmlBatchSize := flag.Int64("dml-batch-size", 10, "batch size for DML events to apply in a single transaction (range 1-100)")
	defaultRetries := flag.Int64("default-retries", 60, "Default number of retries for various operations before panicking")
	cutOverLockTimeoutSeconds := flag.Int64("cut-over-lock-timeout-seconds", 3, "Max number of seconds to hold locks on tables while attempting to cut-over (retry attempted when lock exceeds timeout)")
	niceRatio := flag.Float64("nice-ratio", 0, "force being 'nice', imply sleep time per chunk time; range: [0.0..100.0]. Example values: 0 is aggressive. 1: for every 1ms spent copying rows, sleep additional 1ms (effectively doubling runtime); 0.7: for every 10ms spend in a rowcopy chunk, spend 7ms sleeping immediately after")

	maxLagMillis := flag.Int64("max-lag-millis", 1500, "replication lag at which to throttle operation")

	// 不要再用了，内部实现更好: subsecond resolution
	replicationLagQuery := flag.String("replication-lag-query", "", "Deprecated. gh-ost uses an internal, subsecond resolution query")
	throttleControlReplicas := flag.String("throttle-control-replicas", "", "List of replicas on which to check for lag; comma delimited. Example: myhost1.com:3306,myhost2.com,myhost3.com:3307")
	throttleQuery := flag.String("throttle-query", "", "when given, issued (every second) to check if operation should throttle. Expecting to return zero for no-throttle, >0 for throttle. Query is issued on the migrated server. Make sure this query is lightweight")
	throttleHTTP := flag.String("throttle-http", "", "when given, gh-ost checks given URL via HEAD request; any response code other than 200 (OK) causes throttling; make sure it has low latency response")
	heartbeatIntervalMillis := flag.Int64("heartbeat-interval-millis", 100, "how frequently would gh-ost inject a heartbeat value")
	flag.StringVar(&migrationContext.ThrottleFlagFile, "throttle-flag-file", "", "operation pauses when this file exists; hint: use a file that is specific to the table being altered")
	flag.StringVar(&migrationContext.ThrottleAdditionalFlagFile, "throttle-additional-flag-file", "/tmp/gh-ost.throttle", "operation pauses when this file exists; hint: keep default, use for throttling multiple gh-ost operations")
	flag.StringVar(&migrationContext.PostponeCutOverFlagFile, "postpone-cut-over-flag-file", "", "while this file exists, migration will postpone the final stage of swapping tables, and will keep on syncing the ghost table. Cut-over/swapping would be ready to perform the moment the file is deleted.")
	flag.StringVar(&migrationContext.PanicFlagFile, "panic-flag-file", "", "when this file is created, gh-ost will immediately terminate, without cleanup")

	flag.BoolVar(&migrationContext.DropServeSocket, "initially-drop-socket-file", false, "Should gh-ost forcibly delete an existing socket file. Be careful: this might drop the socket file of a running migration!")
	flag.StringVar(&migrationContext.ServeSocketFile, "serve-socket-file", "", "Unix socket file to serve on. Default: auto-determined and advertised upon startup")
	flag.Int64Var(&migrationContext.ServeTCPPort, "serve-tcp-port", 0, "TCP port to serve on. Default: disabled")

	flag.StringVar(&migrationContext.HooksPath, "hooks-path", "", "directory where hook files are found (default: empty, ie. hooks disabled). Hook files found on this path, and conforming to hook naming conventions will be executed")
	flag.StringVar(&migrationContext.HooksHintMessage, "hooks-hint", "", "arbitrary message to be injected to hooks via GH_OST_HOOKS_HINT, for your convenience")

	// 默认的ServerId
	// XXX: 注意这个很重要，不要和前天的server_id冲突
	flag.UintVar(&migrationContext.ReplicaServerId, "replica-server-id", 99999, "server id used by gh-ost process. Default: 99999")

	isRds := flag.Bool("rds", false, "is rds mysql")

	// 负载控制&检测
	//mysql> show status like "Threads%";
	//+-------------------+-------+
	//| Variable_name     | Value |
	//	+-------------------+-------+
	//| Threads_cached    | 10    |
	//| Threads_connected | 19    |
	//| Threads_created   | 1182  |
	//| Threads_running   | 1     |
	//	+-------------------+-------+
	// 4 rows in set (0.01 sec)
	// 多大的负载才算是大的负载呢?
	//
	// 暂停
	maxLoad := flag.String("max-load", "", "Comma delimited status-name=threshold. e.g: 'Threads_running=100,Threads_connected=500'. When status exceeds threshold, app throttles writes")
	// 中断
	criticalLoad := flag.String("critical-load", "", "Comma delimited status-name=threshold, same format as --max-load. When status exceeds threshold, app panics and quits")
	// 等待一段时间后再中断
	flag.Int64Var(&migrationContext.CriticalLoadIntervalMilliseconds, "critical-load-interval-millis", 0, "When 0, migration immediately bails out upon meeting critical-load. When non-zero, a second check is done after given interval, and migration only bails out if 2nd check still meets critical load")

	quiet := flag.Bool("quiet", false, "quiet")
	verbose := flag.Bool("verbose", false, "verbose")
	debug := flag.Bool("debug", false, "debug mode (very verbose)")
	// stack := flag.Bool("stack", false, "add stack trace upon error")
	help := flag.Bool("help", false, "Display usage")
	version := flag.Bool("version", false, "Print version & exit")
	checkFlag := flag.Bool("check-flag", false, "Check if another flag exists/supported. This allows for cross-version scripting. Exits with 0 when all additional provided flags exist, nonzero otherwise. You must provide (dummy) values for flags that require a value. Example: gh-ost --check-flag --cut-over-lock-timeout-seconds --nice-ratio 0")

	flag.Parse()

	if *checkFlag {
		return
	}
	if *help {
		fmt.Fprintf(os.Stderr, "Usage of gh-ost:\n")
		flag.PrintDefaults()
		return
	}
	if *version {
		appVersion := AppVersion
		if appVersion == "" {
			appVersion = "unversioned"
		}
		fmt.Println(appVersion)
		return
	}

	log.SetLevel(log.LEVEL_ERROR)
	if *verbose {
		log.SetLevel(log.LEVEL_INFO)
	}
	if *debug {
		log.SetLevel(log.LEVEL_DEBUG)
	}
	//if *stack {
	//	log.SetPrintStackTrace(*stack)
	//}
	if *quiet {
		// Override!!
		log.SetLevel(log.LEVEL_ERROR)
	}

	maxLoadValue := *maxLoad
	criticalLoadValue := *criticalLoad
	chunkSizeValue := *chunkSize

	if len(*dbConfigFile) > 0 && base.FileExists(*dbConfigFile) {
		// 读取配置文件
		config, err := base.NewConfigWithFile(*dbConfigFile)
		if err != nil {
			log.Panicf("db config file invalid: %s", *dbConfigFile)
		}
		// 实现alias到db的映射
		db, host, port := config.GetDB(*dbAlias)
		migrationContext.InspectorConnectionConfig.Key.Hostname = host
		migrationContext.InspectorConnectionConfig.Key.Port = port
		migrationContext.DatabaseName = db

		if master, ok := config.SlaveMasterMap[migrationContext.InspectorConnectionConfig.Key.Hostname]; ok {
			migrationContext.AssumeMasterHostname = master
		}

		migrationContext.InitiallyDropGhostTable = config.InitiallyDropGhosTable
		migrationContext.InitiallyDropOldTable = config.InitiallyDropOldTable
		migrationContext.DropServeSocket = config.InitiallyDropSocketFile
		migrationContext.CliUser = config.User
		migrationContext.CliPassword = config.Password

		maxLoadValue = config.MaxLoad
		criticalLoadValue = config.CriticalLoad
		chunkSizeValue = config.ChunkSize

		if config.IsRdsMySQL {
			migrationContext.InspectorConnectionConfig.IsRds = true
			migrationContext.ApplierConnectionConfig.IsRds = true
			migrationContext.AssumeRBR = true // RDS必须这样
		}

	}

	if *isRds {
		migrationContext.InspectorConnectionConfig.IsRds = true
		migrationContext.ApplierConnectionConfig.IsRds = true
	}

	if migrationContext.DatabaseName == "" {
		log.Panicf("--database must be provided and database name must not be empty")
	}

	if migrationContext.OriginalTableName == "" {
		log.Panicf("--table must be provided and table name must not be empty")
	}
	if migrationContext.AlterStatement == "" {
		log.Panicf("--alter must be provided and statement must not be empty")
	}
	migrationContext.Noop = !(*executeFlag)
	if migrationContext.AllowedRunningOnMaster && migrationContext.TestOnReplica {
		log.Panicf("--allow-on-master and --test-on-replica are mutually exclusive")
	}
	if migrationContext.AllowedRunningOnMaster && migrationContext.MigrateOnReplica {
		log.Panicf("--allow-on-master and --migrate-on-replica are mutually exclusive")
	}
	if migrationContext.MigrateOnReplica && migrationContext.TestOnReplica {
		log.Panicf("--migrate-on-replica and --test-on-replica are mutually exclusive")
	}
	if migrationContext.SwitchToRowBinlogFormat && migrationContext.AssumeRBR {
		log.Panicf("--switch-to-rbr and --assume-rbr are mutually exclusive")
	}
	if migrationContext.TestOnReplicaSkipReplicaStop {
		if !migrationContext.TestOnReplica {
			log.Panicf("--test-on-replica-skip-replica-stop requires --test-on-replica to be enabled")
		}
		log.Printf("--test-on-replica-skip-replica-stop enabled. We will not stop replication before cut-over. Ensure you have a plugin that does this.")
	}
	if migrationContext.CliMasterUser != "" && migrationContext.AssumeMasterHostname == "" {
		log.Panicf("--master-user requires --assume-master-host")
	}
	if migrationContext.CliMasterPassword != "" && migrationContext.AssumeMasterHostname == "" {
		log.Panicf("--master-password requires --assume-master-host")
	}
	if *replicationLagQuery != "" {
		log.Printf("--replication-lag-query is deprecated")
	}

	switch *cutOver {
	case "atomic", "default", "":
		migrationContext.CutOverType = base.CutOverAtomic
	case "two-step":
		migrationContext.CutOverType = base.CutOverTwoStep
	default:
		log.Panicf("Unknown cut-over: %s", *cutOver)
	}
	if err := migrationContext.ReadConfigFile(); err != nil {
		log.PanicError(err)
	}
	if err := migrationContext.ReadThrottleControlReplicaKeys(*throttleControlReplicas); err != nil {
		log.PanicError(err)
	}
	if err := migrationContext.ReadMaxLoad(maxLoadValue); err != nil {
		log.PanicError(err)
	}
	if err := migrationContext.ReadCriticalLoad(criticalLoadValue); err != nil {
		log.PanicError(err)
	}
	if migrationContext.ServeSocketFile == "" {
		migrationContext.ServeSocketFile = fmt.Sprintf("/tmp/gh-ost.%s.%s.sock", migrationContext.DatabaseName, migrationContext.OriginalTableName)
	}

	// 如何主动要求输入密码?
	if *askPass {
		fmt.Println("Password:")
		bytePassword, err := terminal.ReadPassword(int(syscall.Stdin))
		if err != nil {
			log.PanicError(err)
		}
		migrationContext.CliPassword = string(bytePassword)
	}
	migrationContext.SetHeartbeatIntervalMilliseconds(*heartbeatIntervalMillis)
	migrationContext.SetNiceRatio(*niceRatio)
	migrationContext.SetChunkSize(chunkSizeValue)
	migrationContext.SetDMLBatchSize(*dmlBatchSize)
	migrationContext.SetMaxLagMillisecondsThrottleThreshold(*maxLagMillis)

	migrationContext.SetThrottleQuery(*throttleQuery)
	migrationContext.SetThrottleHTTP(*throttleHTTP)

	migrationContext.SetDefaultNumRetries(*defaultRetries)
	migrationContext.ApplyCredentials()
	if err := migrationContext.SetCutOverLockTimeoutSeconds(*cutOverLockTimeoutSeconds); err != nil {
		log.ErrorError(err)
	}

	log.Infof("starting gh-ost %+v", AppVersion)
	acceptSignals(migrationContext)

	// 如何执行Migrate呢?
	migrator := logic.NewMigrator()
	err := migrator.Migrate()

	if err != nil {
		migrator.ExecOnFailureHook()
		log.PanicError(err)
	}
	fmt.Fprintf(os.Stdout, "# Done\n")
}
