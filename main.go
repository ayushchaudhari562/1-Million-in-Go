package main

import (
	"fmt"
	"runtime"

	"github.com/valyala/fasthttp"
)

// RequestHandler handles incoming requests with zero allocations
func fastHandler(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Path()) {
	case "/simple":
		ctx.SetContentType("text/plain; charset=utf8")
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBodyString("OK")
	default:
		ctx.Error("Not Found", fasthttp.StatusNotFound)
	}
}

func main() {
	numCores := runtime.NumCPU()
	fmt.Printf("Server started on :8080 using %d CPU cores!\n", numCores)

	// fasthttp Server with optimized settings
	server := &fasthttp.Server{
		Handler:           fastHandler,
		Name:              "1M-RPS-Server",
		Concurrency:       256 * 1024,
		ReadBufferSize:    4096,
		WriteBufferSize:   4096,
		ReduceMemoryUsage: true,
	}

	// fasthttp se listen karo!
	if err := server.ListenAndServe(":8081"); err != nil {
		fmt.Printf("Error starting server: %s\n", err)
	}
}
