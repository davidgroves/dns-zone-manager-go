package main

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestIsAddrInUse(t *testing.T) {
	if isAddrInUse(nil) {
		t.Fatal("nil should not be addr-in-use")
	}
	if isAddrInUse(errors.New("something else")) {
		t.Fatal("unrelated error")
	}
	if !isAddrInUse(syscall.EADDRINUSE) {
		t.Fatal("bare EADDRINUSE")
	}
	wrapped := fmt.Errorf("listen: %w", syscall.EADDRINUSE)
	if !isAddrInUse(wrapped) {
		t.Fatal("wrapped EADDRINUSE")
	}
	op := &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EADDRINUSE}
	if !isAddrInUse(op) {
		t.Fatal("net.OpError EADDRINUSE")
	}
	if !isAddrInUse(fmt.Errorf("outer: %w", op)) {
		t.Fatal("wrapped net.OpError")
	}
}
