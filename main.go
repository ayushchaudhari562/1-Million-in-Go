package main

import (
	"fmt"
	"net/http"
	"runtime"
)

func simpleHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("OK"))
}

func main() {
	// Yeh check karega ki Go kitne CPU cores use kar raha hai
	numCores := runtime.NumCPU()
	fmt.Printf("Go Server started on :8080 using %d CPU cores!\n", numCores)


	http.HandleFunc("/simple", simpleHandler)

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Printf("Error starting server: %s\n", err)
	}
}
