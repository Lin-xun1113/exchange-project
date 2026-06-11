// Package main 提供单元测试入口
package main

import (
	"fmt"
	"testing"
)

// TestMain 是所有测试的入口
func TestMain(m *testing.M) {
	fmt.Println("Running tests...")
	m.Run()
}
