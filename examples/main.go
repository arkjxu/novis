package main

import (
	"fmt"
	"time"

	"github.nike.com/kxu16/novis"
)

func main() {
	server := novis.New(8080, &novis.ProxyOptions{
		Timeout:      30 * time.Second,
		DiscoveryURL: "discovery"})
	err := server.Start(func(n *novis.Novis) {
		fmt.Println("Listening on port: 8080")
	})
	if err != nil {
		panic(err)
	}
}
