package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/gorilla/mux"
)

// Product represents a product with searchable fields
type Product struct {
	ID          int     `json:"id" dynamodbav:"id"`
	Name        string  `json:"name" dynamodbav:"name"`
	Category    string  `json:"category" dynamodbav:"category"`
	Description string  `json:"description" dynamodbav:"description"`
	Brand       string  `json:"brand" dynamodbav:"brand"`
	Price       float64 `json:"price" dynamodbav:"price"`
}

// SearchResponse represents the search result structure
type SearchResponse struct {
	Products     []Product `json:"products"`
	TotalFound   int       `json:"total_found"`
	SearchTime   string    `json:"search_time"`
	CheckedCount int       `json:"checked_count"`
}

// Cart represents a shopping cart (stored in DynamoDB)
type Cart struct {
	UserID    string     `json:"user_id" dynamodbav:"user_id"`
	Items     []CartItem `json:"items" dynamodbav:"items"`
	Total     float64    `json:"total" dynamodbav:"total"`
	UpdatedAt string     `json:"updated_at" dynamodbav:"updated_at"`
	TTL       int64      `json:"ttl,omitempty" dynamodbav:"ttl,omitempty"`
}

// CartItem represents an item in the cart
type CartItem struct {
	ProductID int     `json:"product_id" dynamodbav:"product_id"`
	Quantity  int     `json:"quantity" dynamodbav:"quantity"`
	Price     float64 `json:"price" dynamodbav:"price"`
}

// AddToCartRequest represents the request to add item to cart
type AddToCartRequest struct {
	ProductID int `json:"product_id"`
	Quantity  int `json:"quantity"`
}

// Order represents a completed order (stored in DynamoDB)
type Order struct {
	OrderID   string     `json:"order_id" dynamodbav:"order_id"`
	UserID    string     `json:"user_id" dynamodbav:"user_id"`
	Items     []CartItem `json:"items" dynamodbav:"items"`
	Total     float64    `json:"total" dynamodbav:"total"`
	Status    string     `json:"status" dynamodbav:"status"`
	CreatedAt string     `json:"created_at" dynamodbav:"created_at"`
}

// DynamoDB client and table names
var (
	dynamoClient       *dynamodb.Client
	productsTableName  string
	cartsTableName     string
	ordersTableName    string
	useDynamoDB        bool
)

// In-memory product store for fast search (100k products)
var productStore sync.Map
var productIDs []int

// Fallback in-memory stores when DynamoDB is not available
var cartStore sync.Map
var orderStore sync.Map

// Stats for tracking
var totalRequestsProcessed int64
var totalReadOperations int64
var totalWriteOperations int64
var totalDynamoDBReads int64
var totalDynamoDBWrites int64

// Sample data for variety
var brands = []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta", "Theta"}
var categories = []string{"Electronics", "Books", "Home", "Sports", "Fashion", "Toys", "Garden", "Automotive"}

// initDynamoDB initializes the DynamoDB client
func initDynamoDB() {
	// Get table names from environment variables
	productsTableName = os.Getenv("DYNAMODB_PRODUCTS_TABLE")
	cartsTableName = os.Getenv("DYNAMODB_CARTS_TABLE")
	ordersTableName = os.Getenv("DYNAMODB_ORDERS_TABLE")
	
	// Check if DynamoDB tables are configured
	if cartsTableName == "" || ordersTableName == "" {
		log.Println("⚠️  DynamoDB tables not configured, using in-memory storage")
		useDynamoDB = false
		return
	}
	
	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Printf("⚠️  Failed to load AWS config: %v, using in-memory storage", err)
		useDynamoDB = false
		return
	}
	
	dynamoClient = dynamodb.NewFromConfig(cfg)
	useDynamoDB = true
	log.Println("✅ DynamoDB client initialized successfully")
	log.Printf("   Products Table: %s", productsTableName)
	log.Printf("   Carts Table: %s", cartsTableName)
	log.Printf("   Orders Table: %s", ordersTableName)
}

// initializeProducts generates 100,000 products at startup (in-memory for fast search)
func initializeProducts() {
	log.Println("Generating 100,000 products...")
	start := time.Now()

	productIDs = make([]int, 100000)
	
	for i := 1; i <= 100000; i++ {
		brand := brands[i%len(brands)]
		category := categories[i%len(categories)]
		
		product := Product{
			ID:          i,
			Name:        fmt.Sprintf("Product %s %d", brand, i),
			Category:    category,
			Description: fmt.Sprintf("High-quality %s product from %s brand", category, brand),
			Brand:       brand,
			Price:       float64(10 + (i % 990)),
		}
		
		productStore.Store(i, product)
		productIDs[i-1] = i
	}
	
	elapsed := time.Since(start)
	log.Printf("✅ Generated 100,000 products in %s", elapsed)
}

// DynamoDB Helper Functions

// getCartFromDynamoDB retrieves a cart from DynamoDB
func getCartFromDynamoDB(ctx context.Context, userID string) (*Cart, error) {
	atomic.AddInt64(&totalDynamoDBReads, 1)
	
	result, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(cartsTableName),
		Key: map[string]types.AttributeValue{
			"user_id": &types.AttributeValueMemberS{Value: userID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get cart: %w", err)
	}
	
	if result.Item == nil {
		return nil, nil // Cart not found
	}
	
	var cart Cart
	if err := attributevalue.UnmarshalMap(result.Item, &cart); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cart: %w", err)
	}
	
	return &cart, nil
}

// saveCartToDynamoDB saves a cart to DynamoDB
func saveCartToDynamoDB(ctx context.Context, cart *Cart) error {
	atomic.AddInt64(&totalDynamoDBWrites, 1)
	
	// Set TTL for 7 days (abandoned cart cleanup)
	cart.TTL = time.Now().Add(7 * 24 * time.Hour).Unix()
	cart.UpdatedAt = time.Now().Format(time.RFC3339)
	
	item, err := attributevalue.MarshalMap(cart)
	if err != nil {
		return fmt.Errorf("failed to marshal cart: %w", err)
	}
	
	_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(cartsTableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("failed to save cart: %w", err)
	}
	
	return nil
}

// deleteCartFromDynamoDB removes a cart from DynamoDB
func deleteCartFromDynamoDB(ctx context.Context, userID string) error {
	atomic.AddInt64(&totalDynamoDBWrites, 1)
	
	_, err := dynamoClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(cartsTableName),
		Key: map[string]types.AttributeValue{
			"user_id": &types.AttributeValueMemberS{Value: userID},
		},
	})
	return err
}

// saveOrderToDynamoDB saves an order to DynamoDB
func saveOrderToDynamoDB(ctx context.Context, order *Order) error {
	atomic.AddInt64(&totalDynamoDBWrites, 1)
	
	item, err := attributevalue.MarshalMap(order)
	if err != nil {
		return fmt.Errorf("failed to marshal order: %w", err)
	}
	
	_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(ordersTableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("failed to save order: %w", err)
	}
	
	return nil
}

// getOrderFromDynamoDB retrieves an order from DynamoDB
func getOrderFromDynamoDB(ctx context.Context, orderID string) (*Order, error) {
	atomic.AddInt64(&totalDynamoDBReads, 1)
	
	result, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(ordersTableName),
		Key: map[string]types.AttributeValue{
			"order_id": &types.AttributeValueMemberS{Value: orderID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get order: %w", err)
	}
	
	if result.Item == nil {
		return nil, nil
	}
	
	var order Order
	if err := attributevalue.UnmarshalMap(result.Item, &order); err != nil {
		return nil, fmt.Errorf("failed to unmarshal order: %w", err)
	}
	
	return &order, nil
}

// HTTP Handlers

// searchProducts performs bounded iteration search (READ operation)
func searchProducts(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	
	atomic.AddInt64(&totalRequestsProcessed, 1)
	atomic.AddInt64(&totalReadOperations, 1)
	
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
	
	const maxChecks = 100
	const maxResults = 20
	
	startIdx := 0
	
	for i := 0; i < maxChecks && i < len(productIDs); i++ {
		idx := (startIdx + i) % len(productIDs)
		productID := productIDs[idx]
		
		checkedCount++
		
		if val, ok := productStore.Load(productID); ok {
			product := val.(Product)
			
			nameMatch := strings.Contains(strings.ToLower(product.Name), query)
			categoryMatch := strings.Contains(strings.ToLower(product.Category), query)
			
			if nameMatch || categoryMatch {
				totalFound++
				
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
func healthCheck(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memoryMB := m.Alloc / 1024 / 1024
	
	const memoryThresholdMB = 1500
	
	w.Header().Set("Content-Type", "application/json")
	
	if memoryMB > memoryThresholdMB {
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
	
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "healthy",
		"memory_mb":    memoryMB,
		"dynamodb":     useDynamoDB,
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
		"total_products":     count,
		"memory_footprint":   "~100,000 products",
		"dynamodb_enabled":   useDynamoDB,
		"products_table":     productsTableName,
		"carts_table":        cartsTableName,
		"orders_table":       ordersTableName,
	})
}

// memoryStatsEndpoint shows current memory usage
func memoryStatsEndpoint(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"alloc_mb":            m.Alloc / 1024 / 1024,
		"total_alloc_mb":      m.TotalAlloc / 1024 / 1024,
		"sys_mb":              m.Sys / 1024 / 1024,
		"num_gc":              m.NumGC,
		"requests_processed":  totalRequestsProcessed,
		"read_operations":     totalReadOperations,
		"write_operations":    totalWriteOperations,
		"dynamodb_reads":      totalDynamoDBReads,
		"dynamodb_writes":     totalDynamoDBWrites,
	})
}

// getProduct retrieves a single product by ID (from in-memory store)
func getProduct(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	productID := vars["id"]
	
	var id int
	_, err := fmt.Sscanf(productID, "%d", &id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid product ID"})
		return
	}
	
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

// getCart retrieves user's cart (from DynamoDB or in-memory)
func getCart(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	
	var cart *Cart
	var err error
	
	if useDynamoDB {
		cart, err = getCartFromDynamoDB(r.Context(), userID)
		if err != nil {
			log.Printf("Error getting cart from DynamoDB: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve cart"})
			return
		}
	} else {
		// Fallback to in-memory
		if val, ok := cartStore.Load(userID); ok {
			c := val.(Cart)
			cart = &c
		}
	}
	
	if cart == nil {
		// Return empty cart
		cart = &Cart{
			UserID:    userID,
			Items:     []CartItem{},
			Total:     0,
			UpdatedAt: time.Now().Format(time.RFC3339),
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cart)
}

// addToCart adds an item to cart (DynamoDB or in-memory)
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
	
	var cart *Cart
	
	if useDynamoDB {
		var err error
		cart, err = getCartFromDynamoDB(r.Context(), userID)
		if err != nil {
			log.Printf("Error getting cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve cart"})
			return
		}
	} else {
		if val, ok := cartStore.Load(userID); ok {
			c := val.(Cart)
			cart = &c
		}
	}
	
	if cart == nil {
		cart = &Cart{
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
	cart.UpdatedAt = time.Now().Format(time.RFC3339)
	
	// Save cart
	if useDynamoDB {
		if err := saveCartToDynamoDB(r.Context(), cart); err != nil {
			log.Printf("Error saving cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save cart"})
			return
		}
	} else {
		cartStore.Store(userID, *cart)
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cart)
}

// updateCartItem updates item quantity in cart
func updateCartItem(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	productID := vars["productId"]
	
	id, err := strconv.Atoi(productID)
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
	
	var cart *Cart
	
	if useDynamoDB {
		cart, err = getCartFromDynamoDB(r.Context(), userID)
		if err != nil {
			log.Printf("Error getting cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve cart"})
			return
		}
	} else {
		if val, ok := cartStore.Load(userID); ok {
			c := val.(Cart)
			cart = &c
		}
	}
	
	if cart == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cart not found"})
		return
	}
	
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
	cart.UpdatedAt = time.Now().Format(time.RFC3339)
	
	// Save cart
	if useDynamoDB {
		if err := saveCartToDynamoDB(r.Context(), cart); err != nil {
			log.Printf("Error saving cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save cart"})
			return
		}
	} else {
		cartStore.Store(userID, *cart)
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cart)
}

// checkout creates an order from cart
func checkout(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalWriteOperations, 1)
	
	vars := mux.Vars(r)
	userID := vars["userId"]
	
	var cart *Cart
	var err error
	
	if useDynamoDB {
		cart, err = getCartFromDynamoDB(r.Context(), userID)
		if err != nil {
			log.Printf("Error getting cart: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve cart"})
			return
		}
	} else {
		if val, ok := cartStore.Load(userID); ok {
			c := val.(Cart)
			cart = &c
		}
	}
	
	if cart == nil || len(cart.Items) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cart is empty"})
		return
	}
	
	// Create order
	orderID := fmt.Sprintf("ORD-%s-%d", userID, time.Now().UnixNano())
	order := Order{
		OrderID:   orderID,
		UserID:    userID,
		Items:     cart.Items,
		Total:     cart.Total,
		Status:    "confirmed",
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	
	// Save order and clear cart
	if useDynamoDB {
		if err := saveOrderToDynamoDB(r.Context(), &order); err != nil {
			log.Printf("Error saving order: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create order"})
			return
		}
		
		if err := deleteCartFromDynamoDB(r.Context(), userID); err != nil {
			log.Printf("Warning: Failed to clear cart after checkout: %v", err)
		}
	} else {
		orderStore.Store(orderID, order)
		cartStore.Delete(userID)
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(order)
}

// getOrder retrieves an order
func getOrder(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&totalReadOperations, 1)
	
	vars := mux.Vars(r)
	orderID := vars["orderId"]
	
	var order *Order
	var err error
	
	if useDynamoDB {
		order, err = getOrderFromDynamoDB(r.Context(), orderID)
		if err != nil {
			log.Printf("Error getting order: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve order"})
			return
		}
	} else {
		if val, ok := orderStore.Load(orderID); ok {
			o := val.(Order)
			order = &o
		}
	}
	
	if order == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Order not found"})
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(order)
}

func main() {
	// Initialize DynamoDB client
	initDynamoDB()
	
	// Initialize 100,000 products at startup (in-memory for fast search)
	initializeProducts()
	
	router := mux.NewRouter()

	// READ endpoints
	router.HandleFunc("/products/search", searchProducts).Methods("GET")
	router.HandleFunc("/products/{id}", getProduct).Methods("GET")
	router.HandleFunc("/cart/{userId}", getCart).Methods("GET")
	router.HandleFunc("/orders/{orderId}", getOrder).Methods("GET")
	
	// WRITE endpoints
	router.HandleFunc("/cart/{userId}/items", addToCart).Methods("POST")
	router.HandleFunc("/cart/{userId}/items/{productId}", updateCartItem).Methods("PUT")
	router.HandleFunc("/cart/{userId}/checkout", checkout).Methods("POST")
	
	// Health check and stats endpoints
	router.HandleFunc("/health", healthCheck).Methods("GET")
	router.HandleFunc("/stats", statsEndpoint).Methods("GET")
	router.HandleFunc("/memory", memoryStatsEndpoint).Methods("GET")

	port := ":8080"
	log.Printf("🚀 Starting E-Commerce API server on port %s...", port)
	log.Printf("📦 Storage: DynamoDB=%v", useDynamoDB)
	log.Printf("📖 READ endpoints: /products/search, /products/{id}, /cart/{userId}, /orders/{orderId}")
	log.Printf("✍️  WRITE endpoints: /cart/{userId}/items (POST), /cart/{userId}/items/{productId} (PUT), /cart/{userId}/checkout (POST)")
	log.Fatal(http.ListenAndServe(port, router))
}
