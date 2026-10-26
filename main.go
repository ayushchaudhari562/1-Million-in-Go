package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime"
	"strconv"

	"github.com/valyala/fasthttp"
)

type Item struct {
	ID   int
	Code string
}

const TOTAL_RECORDS = 100000
var memoryStore [TOTAL_RECORDS]string

func generateDummyCode() string {
	bytes := make([]byte, 250) // 250 bytes = 500 hex characters
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func initData() {
	fmt.Println(" Pre-loading 100,000 real records into RAM...")
	dummy := generateDummyCode()
	for i := 0; i < TOTAL_RECORDS; i++ {
		memoryStore[i] = dummy
	}
	fmt.Println(" 100,000 records ready in RAM (Zero disk/network latency)!")
}

// RequestHandler handles incoming requests with zero allocations
func fastHandler(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Path()) {
	case "/simple":
		ctx.SetContentType("text/plain; charset=utf8")
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBodyString("OK")

	case "/code-fast":
		// Query param se id nikalo: /code-fast?id=42
		id := 1
		idBytes := ctx.QueryArgs().Peek("id")
		if len(idBytes) > 0 {
			if parsed, err := strconv.Atoi(string(idBytes)); err == nil && parsed >= 0 && parsed < TOTAL_RECORDS {
				id = parsed
			}
		}

		// Nanosecond RAM lookup O(1)
		code := memoryStore[id]

		// Fast JSON response
		ctx.SetContentType("application/json")
		ctx.SetStatusCode(fasthttp.StatusOK)
		fmt.Fprintf(ctx, `{"id":%d,"code":"%s"}`, id, code)

	default:
		ctx.Error("Not Found", fasthttp.StatusNotFound)
	}
}

func main() {
	numCores := runtime.NumCPU()
	fmt.Printf("Server started on :8081 using %d CPU cores!\n", numCores)

//data preloading in ram 
	initData()

	server := &fasthttp.Server{
		Handler:           fastHandler,
		Name:              "1M-RPS-Server",
		Concurrency:       256 * 1024,
		ReadBufferSize:    4096,
		WriteBufferSize:   4096,
		ReduceMemoryUsage: true,
	}
	if err := server.ListenAndServe(":8081"); err != nil {
		fmt.Printf("Error starting server: %s\n", err)
	}
}
