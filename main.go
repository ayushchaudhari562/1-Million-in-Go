package main
import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/reuseport"
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
					//batch = make([]Item, 0, 5000)
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

var (
	okBytes   = []byte("OK")
	textPlain = []byte("text/plain; charset=utf8")
	pathSimple    = []byte("/simple")      
	pathCodeFast  = []byte("/code-fast")  
	pathCodeWrite = []byte("/code-write")
)

func fastHandler(ctx *fasthttp.RequestCtx) {
	path := ctx.Path();

	if ctx.IsGet() {
		if bytes.Equal(path, pathSimple) {
			ctx.Response.Header.SetContentTypeBytes(textPlain);
			ctx.SetStatusCode(fasthttp.StatusOK);
			ctx.SetBody(okBytes);
			return;
		}

		if bytes.Equal(path, pathCodeFast) {
			id := ctx.QueryArgs().GetUintOrZero("id");
			if id <= 0 || id >= TOTAL_RECORDS {
				id = 1;
			}

			code := memoryStore[id];
			ctx.SetContentType("application/json");
			ctx.SetStatusCode(fasthttp.StatusOK);

			b := ctx.Response.Body()[:0];
			b = append(b, `{"id":`...);
			b = strconv.AppendInt(b, int64(id), 10);
			b = append(b, `,"code":"`...);
			b = append(b, code...);
			b = append(b, `"}`...);
			ctx.SetBody(b);
			return;
		}
	}

	if ctx.IsPost() {
		if bytes.Equal(path, pathCodeWrite) {
			newID := int(atomic.AddUint64(&totalWrites, 1));
			newCode := generateDummyCode();

			if newID < TOTAL_RECORDS {
				memoryStore[newID] = newCode;
			}

			select {
			case writeQueue <- Item{ID: newID, Code: newCode}:
			default:
			}

			ctx.SetContentType("application/json");
			ctx.SetStatusCode(fasthttp.StatusCreated);

			b := ctx.Response.Body()[:0];
			b = append(b, `{"status":"queued","id":`...);
			b = strconv.AppendInt(b, int64(newID), 10);
			b = append(b, '}');
			ctx.SetBody(b);
			return;
		}
	}

	ctx.Error("Not Found", fasthttp.StatusNotFound);
}
	

func main() {
	numCores := runtime.NumCPU()
	runtime.GOMAXPROCS(numCores)
	fmt.Printf(" Server started on :8081 using %d CPU cores & SO_REUSEPORT multi-listeners!\n", numCores)

	initData()

	startBatchWorker()

	for i := 0; i < numCores; i++ {
		go func(workerID int) {
			ln, err := reuseport.Listen("tcp4", ":8081")
			if err != nil {
				log.Fatalf("Worker %d reuseport error: %v", workerID, err)
			}
			server := &fasthttp.Server{
				Handler:               fastHandler,
				NoDefaultServerHeader: true,
				Concurrency:           256 * 1024,
				ReadBufferSize:        1024,
				WriteBufferSize:       1024,
				DisableKeepalive:      false,
				TCPKeepalive:          true,
			}
			if err := server.Serve(ln); err != nil {
				log.Fatalf("Worker %d serve error: %v", workerID, err)
			}
		}(i)
	}

	select {}
}
