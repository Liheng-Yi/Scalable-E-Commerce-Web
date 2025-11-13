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
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Brand       string  `json:"brand"`
	Price       float64 `json:"price"`
}

// SearchResponse represents the search result structure
type SearchResponse struct {
	Products     []Product `json:"products"`
	TotalFound   int       `json:"total_found"`
	SearchTime   string    `json:"search_time"`
	CheckedCount int       `json:"checked_count"` // For debugging: how many products we checked
}

// Cart represents a shopping cart
type Cart struct {
	UserID    string     `json:"user_id"`
	Items     []CartItem `json:"items"`
	Total     float64    `json:"total"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// CartItem represents an item in the cart
type CartItem struct {
	ProductID int     `json:"product_id"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price"`
}

// AddToCartRequest represents the request to add item to cart
type AddToCartRequest struct {
	ProductID int `json:"product_id"`
	Quantity  int `json:"quantity"`
}

// Order represents a completed order
type Order struct {
	OrderID   string     `json:"order_id"`
	UserID    string     `json:"user_id"`
	Items     []CartItem `json:"items"`
	Total     float64    `json:"total"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
}

// Use sync.Map for thread-safe concurrent access
var productStore sync.Map
var productIDs []int // Keep track of all product IDs for iteration

// Cart and order storage (simulates DynamoDB)
var cartStore sync.Map   // key: userID, value: Cart
var orderStore sync.Map  // key: orderID, value: Order

// Stats for tracking
var totalRequestsProcessed int64
var totalReadOperations int64
var totalWriteOperations int64

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
			Price:       float64(10 + (i % 990)), // Prices between $10-$999
		}
		
		productStore.Store(i, product)
		productIDs[i-1] = i
	}
	
	elapsed := time.Since(start)
	log.Printf("✅ Generated 100,000 products in %s", elapsed)
}

// searchProducts performs bounded iteration search (READ operation)
// Critical: Checks EXACTLY 100 products, not all 100,000
func searchProducts(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	
	// Track request count
	atomic.AddInt64(&totalRequestsProcessed, 1)
	atomic.AddInt64(&totalReadOperations, 1)
	
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
		"read_operations":    totalReadOperations,
		"write_operations":   totalWriteOperations,
	})
}

// getProduct retrieves a single product by ID (READ operation - simulates DynamoDB GetItem)
func getProduct(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	productID := vars["id"]
	
	// Parse product ID
	var id int
	_, err := fmt.Sscanf(productID, "%d", &id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid product ID"})
		return
	}
	
	// Load product
	if val, ok := productStore.Load(id); ok {
		product := val.(Product)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(product)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Product not found"})
	}
}

// getCart retrieves user's cart (READ operation - simulates DynamoDB GetItem)
func getCart(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	
	if val, ok := cartStore.Load(userID); ok {
		cart := val.(Cart)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cart)
	} else {
		// Return empty cart
		emptyCart := Cart{
			UserID:    userID,
			Items:     []CartItem{},
			Total:     0,
			UpdatedAt: time.Now(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(emptyCart)
	}
}

// addToCart adds an item to cart (WRITE operation - simulates DynamoDB PutItem)
func addToCart(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	
	var req AddToCartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}
	
	// Validate quantity
	if req.Quantity <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Quantity must be positive"})
		return
	}
	
	// Get product to verify it exists and get price
	val, ok := productStore.Load(req.ProductID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Product not found"})
		return
	}
	product := val.(Product)
	
	// Load or create cart
	var cart Cart
	if val, ok := cartStore.Load(userID); ok {
		cart = val.(Cart)
	} else {
		cart = Cart{
			UserID: userID,
			Items:  []CartItem{},
			Total:  0,
		}
	}
	
	// Check if item already in cart
	found := false
	for i, item := range cart.Items {
		if item.ProductID == req.ProductID {
			cart.Items[i].Quantity += req.Quantity
			found = true
			break
		}
	}
	
	// Add new item if not found
	if !found {
		cart.Items = append(cart.Items, CartItem{
			ProductID: req.ProductID,
			Quantity:  req.Quantity,
			Price:     product.Price,
		})
	}
	
	// Recalculate total
	cart.Total = 0
	for _, item := range cart.Items {
		cart.Total += item.Price * float64(item.Quantity)
	}
	cart.UpdatedAt = time.Now()
	
	// Store updated cart
	cartStore.Store(userID, cart)
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cart)
}

// updateCartItem updates item quantity in cart (WRITE operation - simulates DynamoDB UpdateItem)
func updateCartItem(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	productID := vars["productId"]
	
	var id int
	_, err := fmt.Sscanf(productID, "%d", &id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid product ID"})
		return
	}
	
	var req struct {
		Quantity int `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}
	
	// Load cart
	val, ok := cartStore.Load(userID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cart not found"})
		return
	}
	cart := val.(Cart)
	
	// Update or remove item
	if req.Quantity <= 0 {
		// Remove item
		newItems := []CartItem{}
		for _, item := range cart.Items {
			if item.ProductID != id {
				newItems = append(newItems, item)
			}
		}
		cart.Items = newItems
	} else {
		// Update quantity
		found := false
		for i, item := range cart.Items {
			if item.ProductID == id {
				cart.Items[i].Quantity = req.Quantity
				found = true
				break
			}
		}
		if !found {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "Item not in cart"})
			return
		}
	}
	
	// Recalculate total
	cart.Total = 0
	for _, item := range cart.Items {
		cart.Total += item.Price * float64(item.Quantity)
	}
	cart.UpdatedAt = time.Now()
	
	// Store updated cart
	cartStore.Store(userID, cart)
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cart)
}

// checkout creates an order from cart (WRITE operation - simulates DynamoDB TransactWriteItems)
func checkout(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	
	// Load cart
	val, ok := cartStore.Load(userID)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cart is empty"})
		return
	}
	cart := val.(Cart)
	
	if len(cart.Items) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cart is empty"})
		return
	}
	
	// Create order
	orderID := fmt.Sprintf("ORD-%s-%d", userID, time.Now().Unix())
	order := Order{
		OrderID:   orderID,
		UserID:    userID,
		Items:     cart.Items,
		Total:     cart.Total,
		Status:    "confirmed",
		CreatedAt: time.Now(),
	}
	
	// Store order
	orderStore.Store(orderID, order)
	
	// Clear cart
	cartStore.Delete(userID)
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(order)
}

// getOrder retrieves an order (READ operation - simulates DynamoDB GetItem)
func getOrder(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	orderID := vars["orderId"]
	
	if val, ok := orderStore.Load(orderID); ok {
		order := val.(Order)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(order)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Order not found"})
	}
}

func main() {
	// Initialize 100,000 products at startup
	initializeProducts()
	
	router := mux.NewRouter()

	// READ endpoints (simulate DynamoDB read operations)
	router.HandleFunc("/products/search", searchProducts).Methods("GET")
	router.HandleFunc("/products/{id}", getProduct).Methods("GET")
	router.HandleFunc("/cart/{userId}", getCart).Methods("GET")
	router.HandleFunc("/orders/{orderId}", getOrder).Methods("GET")
	
	// WRITE endpoints (simulate DynamoDB write operations)
	router.HandleFunc("/cart/{userId}/items", addToCart).Methods("POST")
	router.HandleFunc("/cart/{userId}/items/{productId}", updateCartItem).Methods("PUT")
	router.HandleFunc("/cart/{userId}/checkout", checkout).Methods("POST")
	
	// Health check and stats endpoints
	router.HandleFunc("/health", healthCheck).Methods("GET")
	router.HandleFunc("/stats", statsEndpoint).Methods("GET")
	router.HandleFunc("/memory", memoryStatsEndpoint).Methods("GET")

	port := ":8080"
	log.Printf("🚀 Starting E-Commerce API server on port %s...", port)
	log.Printf("📖 READ endpoints: /products/search, /products/{id}, /cart/{userId}, /orders/{orderId}")
	log.Printf("✍️  WRITE endpoints: /cart/{userId}/items (POST), /cart/{userId}/items/{productId} (PUT), /cart/{userId}/checkout (POST)")
	log.Fatal(http.ListenAndServe(port, router))
}


