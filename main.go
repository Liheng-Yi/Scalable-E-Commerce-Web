package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
)

// Product represents a product with searchable fields
type Product struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Brand       string `json:"brand"`
}

// SearchResponse represents the search result structure
type SearchResponse struct {
	Products    []Product `json:"products"`
	TotalFound  int       `json:"total_found"`
	SearchTime  string    `json:"search_time"`
	CheckedCount int      `json:"checked_count"` // For debugging: how many products we checked
}

// Use sync.Map for thread-safe concurrent access
var productStore sync.Map
var productIDs []int // Keep track of all product IDs for iteration

// Stats for tracking
var totalRequestsProcessed int64

// Sample data for variety
var brands = []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta", "Theta"}
var categories = []string{"Electronics", "Books", "Home", "Sports", "Fashion", "Toys", "Garden", "Automotive"}

// initializeProducts generates 100,000 products at startup
func initializeProducts() {
	log.Println("Generating 100,000 products...")
	start := time.Now()

	productIDs = make([]int, 100000)
	
	for i := 1; i <= 100000; i++ {
		brand := brands[i % len(brands)]
		category := categories[i % len(categories)]
		
		product := Product{
			ID:          i,
			Name:        fmt.Sprintf("Product %s %d", brand, i),
			Category:    category,
			Description: fmt.Sprintf("High-quality %s product from %s brand", category, brand),
			Brand:       brand,
		}
		
		productStore.Store(i, product)
		productIDs[i-1] = i
	}
	
	elapsed := time.Since(start)
	log.Printf("✅ Generated 100,000 products in %s", elapsed)
}

// searchProducts performs bounded iteration search
// Critical: Checks EXACTLY 100 products, not all 100,000
func searchProducts(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	
	// Track request count
	atomic.AddInt64(&totalRequestsProcessed, 1)
	
	// Get search query
	query := strings.ToLower(r.URL.Query().Get("q"))
	if query == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Query parameter 'q' is required",
		})
		return
	}

	var results []Product
	totalFound := 0
	checkedCount := 0
	
	// BOUNDED ITERATION: Check ONLY 100 products (simulates fixed computation time)
	const maxChecks = 100
	const maxResults = 20
	
	// Start checking from a random position for variety (optional)
	// Or always start from 0 for consistent behavior
	startIdx := 0
	
	for i := 0; i < maxChecks && i < len(productIDs); i++ {
		idx := (startIdx + i) % len(productIDs)
		productID := productIDs[idx]
		
		// Increment counter for EVERY product checked
		checkedCount++
		
		// Load product from sync.Map
		if val, ok := productStore.Load(productID); ok {
			product := val.(Product)
			
			// Search in name and category (case-insensitive)
			nameMatch := strings.Contains(strings.ToLower(product.Name), query)
			categoryMatch := strings.Contains(strings.ToLower(product.Category), query)
			
			if nameMatch || categoryMatch {
				totalFound++
				
				// Only include in results if we haven't hit max results
				if len(results) < maxResults {
					results = append(results, product)
				}
			}
		}
	}
	
	searchTime := time.Since(startTime)
	
	response := SearchResponse{
		Products:     results,
		TotalFound:   totalFound,
		SearchTime:   fmt.Sprintf("%.3fs", searchTime.Seconds()),
		CheckedCount: checkedCount,
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// healthCheck handles health check with memory threshold monitoring
// If memory exceeds threshold, returns 503 to trigger auto-restart
func healthCheck(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memoryMB := m.Alloc / 1024 / 1024
	
	// Memory threshold for health check failure (1.5GB)
	const memoryThresholdMB = 1500
	
	w.Header().Set("Content-Type", "application/json")
	
	if memoryMB > memoryThresholdMB {
		// Fail health check - ECS will restart the task
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":       "unhealthy",
			"reason":       "memory threshold exceeded",
			"memory_mb":    memoryMB,
			"threshold_mb": memoryThresholdMB,
			"action":       "task will be restarted by ECS",
		})
		log.Printf("🏥 Health check FAILED - Memory: %d MB (threshold: %d MB)", memoryMB, memoryThresholdMB)
		return
	}
	
	// Health check passed
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"memory_mb": memoryMB,
	})
}

// statsEndpoint shows how many products are in memory
func statsEndpoint(w http.ResponseWriter, r *http.Request) {
	count := 0
	productStore.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_products": count,
		"memory_footprint": "~100,000 products",
	})
}

// memoryStatsEndpoint shows current memory usage
func memoryStatsEndpoint(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"alloc_mb":           m.Alloc / 1024 / 1024,
		"total_alloc_mb":     m.TotalAlloc / 1024 / 1024,
		"sys_mb":             m.Sys / 1024 / 1024,
		"num_gc":             m.NumGC,
		"requests_processed": totalRequestsProcessed,
	})
}

func main() {
	// Initialize 100,000 products at startup
	initializeProducts()
	
	router := mux.NewRouter()

	// Search endpoint - THE KEY ENDPOINT FOR PART 2
	router.HandleFunc("/products/search", searchProducts).Methods("GET")
	
	// Health check and stats endpoints
	router.HandleFunc("/health", healthCheck).Methods("GET")
	router.HandleFunc("/stats", statsEndpoint).Methods("GET")
	router.HandleFunc("/memory", memoryStatsEndpoint).Methods("GET")

	port := ":8080"
	log.Printf("🚀 Starting server on port %s...", port)
	log.Printf("📊 Search endpoint: http://localhost%s/products/search?q=electronics", port)
	log.Fatal(http.ListenAndServe(port, router))
}


