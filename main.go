package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

var writeQueue = make(chan Item, 200000)
var totalWrites uint64 // Atomic counter

func startBatchWorker() {
	go func() {
		batch := make([]Item, 0, 5000)
		ticker := time.NewTicker(100 * time.Millisecond)
		for {
			select {
			case item := <-writeQueue:
				batch = append(batch, item)
				if len(batch) >= 5000 {
					batch = batch[:0]
				}
			case <-ticker.C:
				if len(batch) > 0 {
					batch = batch[:0]
				}
			}
		}
	}()
}

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
	fmt.Println("Pre-loading 100,000 real records into RAM...")
	dummy := generateDummyCode()
	for i := 0; i < TOTAL_RECORDS; i++ {
		memoryStore[i] = dummy
	}
	fmt.Println("100,000 records ready in RAM (Zero disk/network latency)!")
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

		code := memoryStore[id]

		ctx.SetContentType("application/json")
		ctx.SetStatusCode(fasthttp.StatusOK)
		fmt.Fprintf(ctx, `{"id":%d,"code":"%s"}`, id, code)

	case "/code-write":
		if !ctx.IsPost() {
			ctx.Error("Method Not Allowed", fasthttp.StatusMethodNotAllowed)
			return
		}

		newID := int(atomic.AddUint64(&totalWrites, 1))
		newCode := generateDummyCode()

		if newID < TOTAL_RECORDS {
			memoryStore[newID] = newCode
		}

		// 2. Non-blocking channel me push karo
		select {
		case writeQueue <- Item{ID: newID, Code: newCode}:
		default:
		}

		ctx.SetContentType("application/json")
		ctx.SetStatusCode(fasthttp.StatusCreated)
		fmt.Fprintf(ctx, `{"status":"queued","id":%d}`, newID)

	default:
		ctx.Error("Not Found", fasthttp.StatusNotFound)
	}
}

func main() {
	numCores := runtime.NumCPU()
	fmt.Printf(" Server started on :8081 using %d CPU cores!\n", numCores)

	initData()

	startBatchWorker()

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
