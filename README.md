# 1 Million RPS in Go: Architecture, Benchmarks & Deep Internals

A comprehensive technical study and benchmark progression comparing **Node.js** vs **Go (Golang)** on identical 10-Core (16-Thread) hardware, scaling from baseline to 1 Million Requests/Second.

---

## 1. The Real Comparison: Node.js vs Go Progression

| Stage / Setup | Node.js (Cpeak / Express) | Go (Golang Native) | What Made the Difference? |
| :--- | :--- | :--- | :--- |
| **1-Core Initial Baseline** | **32,680 RPS** (`GET /simple`) | **~88,454 RPS** (1 Client worker) | Go compiled binary vs V8 JIT interpreter |
| **Full Machine (Multi-Core)** | **74,918 RPS** (16 PM2 Workers) | **196,480 RPS (Peak 203K)** | Go GMP work-stealing vs 16 heavy Node processes |
| **Peak Load Generator (`-w 4`)** | **104,218 RPS** (Maxed out) | **203,007 RPS** | 2x higher throughput with 53x less RAM |
| **PostgreSQL Unindexed** | **103.8 RPS** (`ORDER BY RANDOM`) | *(To be benchmarked in Go)* | Disk I/O & table sequential scan bottleneck |
| **PostgreSQL B-Tree Indexed** | **7,509.6 RPS** (`WHERE id = $1`) | *(To be benchmarked in Go)* | B-Tree O(log N) lookup; DB pool concurrency |
| **Redis In-Memory Reads** | **48,062.5 RPS** (Redis 8 Multi-thread) | *(Target: 250K+ in Go)* | Go's zero-copy TCP socket buffer handling |
| **RAM Consumption** | **~800 MB** (16 x V8 heaps) | **~15 MB Total** | Goroutines (2KB) vs V8 Node processes (50MB each) |

---

## 2. Architecture Comparison: Why Go Crushed Node.js

![Software Architecture Comparison: Node.js PM2 Cluster vs Golang GMP Model](./img/architecture.jpg)

![Node.js PM2 Cluster vs Go GMP Scheduler Comparison](./img/pm2_vs_gmp.jpg)

---

## 3. Deep-Dive: The 4 Core Mechanics Behind Go's Speed

### 1. Goroutines vs OS Processes (Memory Footprint)
* **Node.js**: Relies on a single-threaded libuv event loop per process. 
  * 1 Core = **32.6K RPS**.
  * To use 10 cores, you MUST run 16 separate PM2 OS processes. 16 processes consume **~800MB RAM** just for the V8 engine and runtime heaps!
* **Go**: Uses **Goroutines**.
  * Starts with only **2 KB stack per request**.
  * 1 Single binary uses **~15MB RAM** total and serves **203K RPS**!

### 2. The GMP Work-Stealing Scheduler
Go manages concurrency in user space using the GMP model:
* **G (Goroutine)**: The execution task (2KB stack).
* **M (Machine)**: The actual OS thread executing code.
* **P (Processor)**: The logical processor context (matches CPU physical cores, here 10).

**The Work-Stealing Mechanism**:
If Core 0 (P0) finishes its requests and its local queue is empty, it does not sit idle. It instantly steals **50% of the pending Goroutines** from Core 1's queue. In Node.js, if one PM2 worker is overloaded, another worker cannot steal its callbacks without complex IPC load-balancing.

### 3. Go Netpoller (Kernel I/O Multiplexing)
When a Goroutine reads from or writes to a network socket (`http.Request`), Go does not block the underlying OS thread:
* Go parks the Goroutine in the **Netpoller** (using `IOCP` on Windows, `epoll` on Linux, `kqueue` on macOS).
* The OS thread (`M`) immediately picks up another Goroutine.
* As soon as network packets arrive from the network card, the Netpoller wakes the Goroutine and puts it back into an active CPU core's run queue.

### 4. Zero IPC (Inter-Process Communication) Overhead
* In Node.js, 16 workers cannot share in-memory data structures directly. To share cache or state, they must talk over Redis or OS sockets (introducing latency).
* In Go, all 10 cores run within **one single shared memory space**. Channels, atomic pointers, and `sync.Map` communicate at hardware memory bus speeds (nanoseconds).

---

## 4. Benchmark Progression Roadmap to 1 Million RPS

| Milestone | Architecture / Layer | Target RPS | Status |
| :--- | :--- | :--- | :--- |
| **Milestone 1** | Standard `net/http` Baseline (10 Cores) | **196,480 RPS (Peak 203K)** | **VERIFIED** |
| **Milestone 2** | `fasthttp` (Zero-Memory Buffer Pooling) | **350,000 - 450,000 RPS** | Up Next |
| **Milestone 3** | In-Process In-Memory Cache (`BigCache`) | **500,000+ RPS** | Pending |
| **Milestone 4** | Redis Multi-Threaded Pipeline + Worker Pools | **250,000+ Cached DB RPS**| Pending |
| **Milestone 5** | Linux/WSL2 Kernel Tuning + `wrk` C Benchmarker | **1,000,000 RPS (1M Target)**| Final Goal |

---

## 5. Verified Milestone 1 Benchmark Output

Executed on October 26, 2026:
```text
Running 10s test @ http://localhost:8080/simple
100 connections with 10 pipelining factor
4 workers

Stat         Avg         Stdev       Max
Latency      4.64 ms     5.15 ms     61 ms
Req/Sec      196,480     8,525.85    203,007
Bytes/Sec    23.2 MB     1.01 MB     24 MB

1,966k requests in 10.11s, 232 MB read
```

---

## 6. How to Reproduce Milestone 1

1. Start the Go server:
   ```bash
   go run main.go
   ```
2. Run Autocannon with 4 parallel client workers and 100 pipelined connections:
   ```bash
   npx autocannon -c 100 -d 10 -p 10 -w 4 http://localhost:8080/simple
   ```
