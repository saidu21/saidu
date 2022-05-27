/*
   Copyright 2022 GitHub Inc.
	 See https://github.com/github/gh-ost/blob/master/LICENSE
*/

package mysql

import (
	"errors"

	gomysql "github.com/go-mysql-org/go-mysql/mysql"
)

// GTIDBinlogCoordinates describe binary log coordinates in MySQL GTID format.
type GTIDBinlogCoordinates struct {
	GTIDSet *gomysql.MysqlGTIDSet
	UUIDSet *gomysql.UUIDSet
}

// NewGTIDBinlogCoordinates parses a MySQL GTID set into a *GTIDBinlogCoordinates struct.
func NewGTIDBinlogCoordinates(gtidSet string) (*GTIDBinlogCoordinates, error) {
	set, err := gomysql.ParseMysqlGTIDSet(gtidSet)
	return &GTIDBinlogCoordinates{
		GTIDSet: set.(*gomysql.MysqlGTIDSet),
	}, err
}

// DisplayString returns a user-friendly string representation of these current UUID set or the full GTID set.
func (this *GTIDBinlogCoordinates) DisplayString() string {
	if this.UUIDSet != nil {
		return this.UUIDSet.String()
	}
	return this.String()
}

// String returns a user-friendly string representation of these full GTID set.
func (this GTIDBinlogCoordinates) String() string {
	return this.GTIDSet.String()
}

// Equals tests equality of this coordinate and another one.
func (this *GTIDBinlogCoordinates) Equals(other BinlogCoordinates) bool {
	if other == nil || this.IsEmpty() || other.IsEmpty() {
		return false
	}

	otherBinlogCoordinates := &GTIDBinlogCoordinates{}
	if err := binlogCoordinatesToImplementation(other, otherBinlogCoordinates); err != nil {
		panic(err)
	}

	return this.GTIDSet.Equal(otherBinlogCoordinates.GTIDSet)
}

// IsEmpty returns true if the GTID set is empty.
func (this *GTIDBinlogCoordinates) IsEmpty() bool {
	return this.GTIDSet == nil
}

// SmallerThan returns true if this coordinate is strictly smaller than the other.
func (this *GTIDBinlogCoordinates) SmallerThan(other BinlogCoordinates) bool {
	otherBinlogCoordinates := &GTIDBinlogCoordinates{}
	if err := binlogCoordinatesToImplementation(other, otherBinlogCoordinates); err != nil {
		panic(err)
	}

	// if 'this' does not contain the same sets we assume we are behind 'other'.
	// there are probably edge cases where this isn't true
	return !this.GTIDSet.Contain(other.GTIDSet)
}

// SmallerThanOrEquals returns true if this coordinate is the same or equal to the other one.
func (this *GTIDBinlogCoordinates) SmallerThanOrEquals(other BinlogCoordinates) bool {
	return this.Equals(other) || this.SmallerThan(other)
}

func (this *GTIDBinlogCoordinates) Update(update interface{}) error {
	switch u := update.(type) {
	case *gomysql.UUIDSet:
		this.GTIDSet.AddSet(u)
		this.UUIDSet = u
	case *gomysql.MysqlGTIDSet:
		this.GTIDSet = u
	default:
		return errors.New("unsupported update")
	}
	return nil
}
