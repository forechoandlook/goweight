package main

import (
	"fmt"
	"time"
	
	"github.com/dustin/go-humanize"
)

func main() {
	fmt.Println("Hello from test app!")
	
	// 使用一些库函数
	size := uint64(1024 * 1024 * 10) // 10MB
	fmt.Printf("Size: %s\n", humanize.Bytes(size))
	
	time.Sleep(100 * time.Millisecond)
}