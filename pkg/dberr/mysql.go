// Package dberr classifies errors returned by MySQL.
package dberr

import (
	"errors"

	"github.com/go-sql-driver/mysql"
)

// IsDuplicate reports whether err is a duplicate-key error.
func IsDuplicate(err error) bool { return number(err) == 1062 }

// IsCheckViolation reports whether err is a CHECK constraint violation.
func IsCheckViolation(err error) bool { return number(err) == 3819 }

// IsDeadlock reports whether err is a deadlock or lock wait timeout.
func IsDeadlock(err error) bool {
	n := number(err)
	return n == 1213 || n == 1205
}

func number(err error) uint16 {
	var e *mysql.MySQLError
	if errors.As(err, &e) && e != nil {
		return e.Number
	}
	return 0
}
