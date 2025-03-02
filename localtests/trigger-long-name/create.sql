-- Drop both original and ghost triggers to ensure a clean slate
drop trigger if exists this_is_a_very_long_trigger_name_that_will_exceed_limit_with_suffix;
drop trigger if exists this_is_a_very_long_trigger_name_that_will_exceed_limit_with_suffix_ghost_suffix;

drop table if exists gh_ost_test_log;
drop table if exists gh_ost_test;
create table gh_ost_test (
  id int auto_increment,
  i int not null,
  color varchar(32),
  primary key(id)
) auto_increment=1;

create table gh_ost_test_log (
  id int auto_increment,
  test_id int,
  action varchar(16),
  ts timestamp default current_timestamp,
  primary key(id)
);

-- Create a trigger with a very long name (just under the 64 character limit)
delimiter ;;
create trigger this_is_a_very_long_trigger_name_that_will_exceed_limit_with_suffix after insert on gh_ost_test
for each row
begin
  insert into gh_ost_test_log (test_id, action) values (NEW.id, 'INSERT');
end ;;
delimiter ;

drop event if exists gh_ost_test;
delimiter ;;
create event gh_ost_test
  on schedule every 1 second
  starts current_timestamp
  ends current_timestamp + interval 60 second
  on completion not preserve
  enable
  do
begin
  insert into gh_ost_test values (null, 11, 'red');
  insert into gh_ost_test values (null, 13, 'green');
  insert into gh_ost_test values (null, 17, 'blue');
end ;;
delimiter ; 