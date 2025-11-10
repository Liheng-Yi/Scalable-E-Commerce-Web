# E-commerce Product API

A simple Go-based REST API for managing products in an online store. This implementation follows the OpenAPI 3.0.3 specification provided in `api.yaml`.

## Features

- **GET /products/{productId}** - Retrieve product details by ID
- **POST /products/{productId}/details** - Create or update product information
- In-memory storage with thread-safe operations
- Full input validation according to OpenAPI specification
- Proper HTTP status codes and error responses
- Docker containerization support

## Product Schema

```json
{
  "product_id": 12345,
  "sku": "ABC-123-XYZ",
  "manufacturer": "Acme Corporation",
  "category_id": 456,
  "weight": 1250,
  "some_other_id": 789
}
```

### Field Constraints
- `product_id`: integer, minimum 1
- `sku`: string, 1-100 characters
- `manufacturer`: string, 1-200 characters
- `category_id`: integer, minimum 1
- `weight`: integer, minimum 0 (in grams)
- `some_other_id`: integer, minimum 1

## Prerequisites

- Go 1.21 or higher
- Docker (optional, for containerization)

## Installation & Running

### Local Development

1. Clone the repository and navigate to the week5 directory:
```bash
cd week5
```

2. Download dependencies:
```bash
go mod download
```

3. Run the server:
```bash
go run main.go
```

The server will start on `http://localhost:8080`

### Using Docker

1. Build the Docker image:
```bash
docker build -t product-api .
```

2. Run the container:
```bash
docker run -p 8080:8080 product-api
```

## API Examples

### Health Check

Check if the server is running:

```bash
curl http://localhost:8080/health
```

**Response:**
```json
{
  "status": "healthy"
}
```

### Create/Update a Product

Add product details (note: product_id in path must match product_id in body):

```bash
curl -X POST http://localhost:8080/products/12345/details \
  -H "Content-Type: application/json" \
  -d '{
    "product_id": 12345,
    "sku": "ABC-123-XYZ",
    "manufacturer": "Acme Corporation",
    "category_id": 456,
    "weight": 1250,
    "some_other_id": 789
  }'
```

**Success Response:** HTTP 204 No Content

**Error Response Example (400 Bad Request):**
```json
{
  "error": "INVALID_INPUT",
  "message": "Invalid product data",
  "details": "sku must be between 1 and 100 characters"
}
```

### Get Product by ID

Retrieve a product's details:

```bash
curl http://localhost:8080/products/12345
```

**Success Response (200 OK):**
```json
{
  "product_id": 12345,
  "sku": "ABC-123-XYZ",
  "manufacturer": "Acme Corporation",
  "category_id": 456,
  "weight": 1250,
  "some_other_id": 789
}
```

**Error Response (404 Not Found):**
```json
{
  "error": "NOT_FOUND",
  "message": "Product not found",
  "details": "Product with ID 12345 does not exist"
}
```

### More Examples

#### Invalid Product ID (Non-numeric)
```bash
curl http://localhost:8080/products/abc
```

**Response (400 Bad Request):**
```json
{
  "error": "INVALID_INPUT",
  "message": "Invalid product ID",
  "details": "Product ID must be a positive integer"
}
```

#### Invalid Product ID (Zero or Negative)
```bash
curl http://localhost:8080/products/0
```

**Response (400 Bad Request):**
```json
{
  "error": "INVALID_INPUT",
  "message": "Invalid product ID",
  "details": "Product ID must be a positive integer"
}
```

#### Product ID Mismatch
```bash
curl -X POST http://localhost:8080/products/12345/details \
  -H "Content-Type: application/json" \
  -d '{
    "product_id": 99999,
    "sku": "ABC-123-XYZ",
    "manufacturer": "Acme Corporation",
    "category_id": 456,
    "weight": 1250,
    "some_other_id": 789
  }'
```

**Response (400 Bad Request):**
```json
{
  "error": "INVALID_INPUT",
  "message": "Product ID mismatch",
  "details": "Product ID in path (12345) does not match product ID in body (99999)"
}
```

## HTTP Status Codes

The API uses standard HTTP status codes:

- **200 OK** - Successful GET request
- **204 No Content** - Successful POST request
- **400 Bad Request** - Invalid input data
- **404 Not Found** - Product not found
- **500 Internal Server Error** - Server error

Learn more about HTTP status codes at [HTTP Cats](https://http.cat/)! 🐱

## Automated Testing

### Using the Test Script

**Option 1: Simple test script (no dependencies)**
```bash
chmod +x test_api_simple.sh
./test_api_simple.sh
```

**Option 2: Test script with jq for formatted output**

If you have `jq` installed:
```bash
chmod +x test_api.sh
./test_api.sh
```

To install `jq`:
- **Windows (Git Bash)**: Download from https://jqlang.github.io/jq/download/
- **macOS**: `brew install jq`
- **Linux**: `sudo apt-get install jq` or `sudo yum install jq`

## Testing with Postman

You can import these curl commands into Postman for easier testing:

1. Open Postman
2. Click "Import" → "Raw text"
3. Paste any of the curl commands above
4. Click "Import"

Or import the included `Product_API.postman_collection.json` file directly into Postman.

**The Postman collection includes automated test scripts!** See `POSTMAN_TESTING_GUIDE.md` for details on:
- 40+ automated tests covering all scenarios
- Running tests individually or as a collection
- CI/CD integration with Newman
- Understanding test results

## Project Structure

```
week5/
├── main.go                              # Main application code
├── go.mod                               # Go module dependencies
├── go.sum                               # Go dependency checksums
├── Dockerfile                           # Docker container definition
├── docker-compose.yml                   # Docker Compose configuration
├── api.yaml                             # OpenAPI specification
├── README.md                            # This file
├── POSTMAN_TESTING_GUIDE.md             # Guide for automated testing
├── Product_API.postman_collection.json  # Postman collection with tests
├── test_api_simple.sh                   # Bash test script (no dependencies)
└── .gitignore                           # Git ignore file
```

## Implementation Details

- **Thread-Safe Storage**: Uses `sync.RWMutex` for concurrent access to the in-memory product store
- **Input Validation**: All fields are validated according to OpenAPI spec constraints
- **Error Handling**: Comprehensive error responses with meaningful messages
- **RESTful Design**: Follows REST principles and HTTP standards
- **Gorilla Mux Router**: Uses the popular Gorilla Mux for flexible routing

## Future Enhancements

This is a simple Product API implementation. Future versions could include:
- Database persistence (PostgreSQL, MongoDB, etc.)
- Authentication and authorization
- Additional CRUD operations (DELETE, PUT)
- Pagination for listing products
- Search and filter capabilities
- Integration with other services (Shopping Cart, Warehouse, Payments)

## License

MIT License


