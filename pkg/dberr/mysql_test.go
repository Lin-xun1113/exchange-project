package dberr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestClassifyMySQLError(t *testing.T) {
	for _, test := range []struct {
		number                      uint16
		duplicate, check, retryable bool
	}{
		{1062, true, false, false},
		{3819, false, true, false},
		{1213, false, false, true},
		{1205, false, false, true},
		{2000, false, false, false},
	} {
		err := fmt.Errorf("query failed: %w", &mysql.MySQLError{Number: test.number})
		if got := IsDuplicate(err); got != test.duplicate {
			t.Errorf("IsDuplicate for %d = %v", test.number, got)
		}
		if got := IsCheckViolation(err); got != test.check {
			t.Errorf("IsCheckViolation for %d = %v", test.number, got)
		}
		if got := IsDeadlock(err); got != test.retryable {
			t.Errorf("IsDeadlock for %d = %v", test.number, got)
		}
	}
	for _, err := range []error{nil, errors.New("not a MySQL error")} {
		if IsDuplicate(err) || IsCheckViolation(err) || IsDeadlock(err) {
			t.Errorf("misclassified %v", err)
		}
	}
}
