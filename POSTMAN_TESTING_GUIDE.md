# Postman Testing Guide

## Importing the Collection

1. Open Postman
2. Click **Import** button
3. Select `Product_API.postman_collection.json`
4. Click **Import**

## Understanding the Test Scripts

Each request in the collection now includes automated test scripts that run after the response is received. The tests validate:

### ✅ What Gets Tested

#### Health Check
- ✓ Status code is 200
- ✓ Response has `status` field with value "healthy"
- ✓ Content-Type is application/json

#### Create Product (POST)
- ✓ Status code is 204 No Content
- ✓ Response time is under 500ms
- ✓ Stores product ID for later tests

#### Get Product (GET)
- ✓ Status code is 200
- ✓ Content-Type is application/json
- ✓ Product has all required fields (product_id, sku, manufacturer, category_id, weight, some_other_id)
- ✓ Product ID matches the requested ID
- ✓ All fields have correct data types
- ✓ Product data matches expected values

#### Error Responses (404, 400)
- ✓ Correct status codes
- ✓ Error response structure (error, message fields)
- ✓ Correct error codes (NOT_FOUND, INVALID_INPUT)
- ✓ Descriptive error messages

## Running Tests

### Option 1: Run Individual Requests

1. Select a request from the collection
2. Click **Send**
3. Check the **Test Results** tab at the bottom
4. Green checkmarks (✓) = passed tests
5. Red X = failed tests with details

### Option 2: Run Entire Collection

1. Click the **three dots (...)** next to "Product API" collection
2. Select **Run collection**
3. Click **Run Product API**
4. View all test results in the **Runner** tab

### Option 3: Automate with Newman (CLI)

Install Newman (Postman's CLI):
```bash
npm install -g newman
```

Run the collection:
```bash
newman run Product_API.postman_collection.json
```

## Test Execution Order

For best results, run tests in this order:

1. **Health Check** - Verify server is running
2. **Create Product** - Create product ID 12345
3. **Get Product** - Verify it was created
4. **Create Product - Electronics** - Create another product
5. **Update Existing Product** - Update product 12345
6. **Error Tests** - Run all error scenario tests

## Reading Test Results

### Successful Test Example
```
PASS ✓ Status code is 200
PASS ✓ Content-Type is application/json
PASS ✓ Product has all required fields
```

### Failed Test Example
```
FAIL ✗ Status code is 200
  AssertionError: expected 404 to equal 200
```

## Collection Variables

The collection uses variables to pass data between requests:
- `product_id` - Stores created product ID
- `updated_sku` - Stores updated SKU for verification

View/edit variables:
1. Click on the collection name
2. Select **Variables** tab

## Advanced Testing Features

### Chain Requests
Tests can store values for use in subsequent requests:
```javascript
pm.collectionVariables.set("product_id", 12345);
```

### Conditional Tests
Tests adapt based on responses:
```javascript
if (pm.response.code === 200) {
    pm.test("Product exists", function() {
        // validation logic
    });
}
```

### Response Time Checks
```javascript
pm.test("Response time is acceptable", function () {
    pm.expect(pm.response.responseTime).to.be.below(500);
});
```

## Troubleshooting

### All Tests Fail
- **Issue**: Server not running
- **Fix**: Start the server with `go run main.go` or `docker-compose up`

### "Product not found" Tests Pass When They Shouldn't
- **Issue**: Product already exists in memory
- **Fix**: Restart the server to clear in-memory storage

### Connection Errors
- **Issue**: Wrong URL or port
- **Fix**: Verify server is on `http://localhost:8080`

## CI/CD Integration

You can integrate these tests into your CI/CD pipeline:

### GitHub Actions Example
```yaml
- name: Run API Tests
  run: |
    docker-compose up -d
    sleep 5
    newman run Product_API.postman_collection.json
    docker-compose down
```

### Jenkins Example
```groovy
stage('API Tests') {
    steps {
        sh 'newman run Product_API.postman_collection.json --reporters cli,json'
    }
}
```

## Extending Tests

To add your own tests to any request:

1. Select the request
2. Go to the **Tests** tab
3. Add JavaScript test code:

```javascript
pm.test("Your test name", function () {
    var jsonData = pm.response.json();
    pm.expect(jsonData.field).to.eql("expected value");
});
```

## Common Test Patterns

### Check Status Code
```javascript
pm.test("Status code is 200", function () {
    pm.response.to.have.status(200);
});
```

### Validate JSON Property Exists
```javascript
pm.test("Has product_id field", function () {
    var jsonData = pm.response.json();
    pm.expect(jsonData).to.have.property('product_id');
});
```

### Validate Data Type
```javascript
pm.test("product_id is a number", function () {
    var jsonData = pm.response.json();
    pm.expect(jsonData.product_id).to.be.a('number');
});
```

### Validate Value
```javascript
pm.test("SKU matches", function () {
    var jsonData = pm.response.json();
    pm.expect(jsonData.sku).to.eql('ABC-123-XYZ');
});
```

### Check Response Header
```javascript
pm.test("Content-Type is JSON", function () {
    pm.response.to.have.header("Content-Type", /application\\/json/);
});
```

## Resources

- [Postman Test Scripts Documentation](https://learning.postman.com/docs/writing-scripts/test-scripts/)
- [Chai Assertion Library](https://www.chaijs.com/api/bdd/) (used by Postman)
- [Newman CLI](https://www.npmjs.com/package/newman)

## Summary

Your Postman collection now includes **11 requests** with **40+ automated tests** covering:
- ✅ Success scenarios (200, 204)
- ✅ Error handling (400, 404)
- ✅ Data validation
- ✅ Type checking
- ✅ Performance monitoring
- ✅ API contract compliance

Happy testing! 🚀

