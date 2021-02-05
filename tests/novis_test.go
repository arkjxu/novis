package main

import (
	"testing"
	"time"

	"github.nike.com/kxu16/novis"
)

func TestNovisShouldStartUp(t *testing.T) {
	server := novis.New(8080, nil)
	_ = server.Start(func(n *novis.Novis) {
		time.Sleep(3 * time.Second)
		_ = n.Close()
	})
}
