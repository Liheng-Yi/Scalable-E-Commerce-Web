#!/bin/bash
# Failure Recovery Testing Script for AWS Deployment
# Tests Circuit Breaker and Retry patterns on deployed ECS service

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

echo ""
echo -e "${CYAN}=========================================="
echo "  Failure Recovery Testing on AWS"
echo -e "==========================================${NC}"
echo ""

# Get ALB URL
ALB_URL=$(terraform output -raw alb_dns_name 2>/dev/null)

if [ -z "$ALB_URL" ]; then
    echo -e "${RED}❌ Could not get ALB URL. Make sure you've deployed with Terraform first.${NC}"
    echo ""
    echo "Run: ./deploy.sh"
    exit 1
fi

BASE_URL="http://$ALB_URL"
echo -e "${GREEN}✅ ALB URL: $BASE_URL${NC}"
echo ""

# Function to make API calls with pretty output
call_api() {
    local method=$1
    local endpoint=$2
    local data=$3
    local description=$4
    
    echo -e "${BLUE}▶ $description${NC}"
    echo -e "  ${CYAN}$method $endpoint${NC}"
    
    if [ -n "$data" ]; then
        echo -e "  Body: $data"
        response=$(curl -s -X $method "$BASE_URL$endpoint" \
            -H "Content-Type: application/json" \
            -d "$data")
    else
        response=$(curl -s -X $method "$BASE_URL$endpoint")
    fi
    
    echo -e "  ${GREEN}Response:${NC}"
    echo "$response" | jq . 2>/dev/null || echo "$response"
    echo ""
}

# Function to pause for user
pause() {
    echo -e "${YELLOW}Press Enter to continue...${NC}"
    read
}

# ============================================
# STEP 1: Health Check and Initial Status
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 1: Initial Health Check"
echo -e "==========================================${NC}"
echo ""

call_api "GET" "/health" "" "Checking service health"
call_api "GET" "/resilience/circuit-breakers" "" "Viewing initial circuit breaker status"

pause

# ============================================
# STEP 2: Test Retry Pattern
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 2: Testing Retry Pattern"
echo -e "==========================================${NC}"
echo ""

echo -e "${YELLOW}Testing retry with 2 simulated failures (should succeed after retries)${NC}"
call_api "POST" "/resilience/test-retry" '{"fail_count": 2, "max_retries": 3}' "Testing retry pattern (2 failures, 3 max retries)"

echo -e "${YELLOW}Testing retry with 5 failures (should fail - exceeds max retries)${NC}"
call_api "POST" "/resilience/test-retry" '{"fail_count": 5, "max_retries": 3}' "Testing retry exhaustion (5 failures, 3 max retries)"

pause

# ============================================
# STEP 3: Prepare Cart for Checkout Test
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 3: Prepare Test Data"
echo -e "==========================================${NC}"
echo ""

USER_ID="test-user-$(date +%s)"
echo -e "${YELLOW}Creating test user cart: $USER_ID${NC}"

call_api "POST" "/cart/$USER_ID/items" '{"product_id": 100, "quantity": 2}' "Adding product 100 to cart"
call_api "POST" "/cart/$USER_ID/items" '{"product_id": 200, "quantity": 1}' "Adding product 200 to cart"
call_api "GET" "/cart/$USER_ID" "" "Viewing cart contents"

pause

# ============================================
# STEP 4: Enable Payment Failure Simulation
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 4: Enable Payment Failures (80%)"
echo -e "==========================================${NC}"
echo ""

call_api "POST" "/resilience/simulate/payment" '{"enable": true, "failure_rate": 0.8}' "Enabling 80% payment failure rate"
call_api "GET" "/resilience/circuit-breakers" "" "Checking circuit breaker status after enabling failures"

pause

# ============================================
# STEP 5: Test Checkout with Failures
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 5: Attempting Checkout (with failures)"
echo -e "==========================================${NC}"
echo ""

echo -e "${YELLOW}This may succeed after retries, or fail and trigger circuit breaker${NC}"
echo ""

for i in 1 2 3 4 5; do
    echo -e "${BLUE}━━━━━ Checkout Attempt #$i ━━━━━${NC}"
    
    # Create new cart for each attempt
    CART_USER="user-attempt-$i-$(date +%s)"
    
    # Add item silently
    curl -s -X POST "$BASE_URL/cart/$CART_USER/items" \
        -H "Content-Type: application/json" \
        -d '{"product_id": 100, "quantity": 1}' > /dev/null
    
    # Attempt checkout
    response=$(curl -s -X POST "$BASE_URL/cart/$CART_USER/checkout")
    
    echo "$response" | jq . 2>/dev/null || echo "$response"
    echo ""
    
    # Check if circuit is open
    status=$(echo "$response" | jq -r '.circuit_breaker // empty' 2>/dev/null)
    if [ "$status" = "open" ]; then
        echo -e "${RED}⚡ Circuit Breaker is now OPEN!${NC}"
        break
    fi
    
    sleep 1
done

pause

# ============================================
# STEP 6: Check Circuit Breaker Status
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 6: Circuit Breaker Status"
echo -e "==========================================${NC}"
echo ""

call_api "GET" "/resilience/circuit-breakers" "" "Viewing circuit breaker status after failures"

pause

# ============================================
# STEP 7: Test Request Rejection (Circuit Open)
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 7: Testing Circuit Open Behavior"
echo -e "==========================================${NC}"
echo ""

echo -e "${YELLOW}If circuit is OPEN, this request should be rejected immediately${NC}"

# Create cart and try checkout
REJECT_USER="reject-test-$(date +%s)"
curl -s -X POST "$BASE_URL/cart/$REJECT_USER/items" \
    -H "Content-Type: application/json" \
    -d '{"product_id": 100, "quantity": 1}' > /dev/null

call_api "POST" "/cart/$REJECT_USER/checkout" "" "Attempting checkout with circuit potentially open"

pause

# ============================================
# STEP 8: Reset and Recovery
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 8: Reset Circuit Breaker"
echo -e "==========================================${NC}"
echo ""

call_api "POST" "/resilience/reset/payment" "" "Resetting payment circuit breaker"
call_api "GET" "/resilience/circuit-breakers" "" "Verifying circuit is CLOSED"

pause

# ============================================
# STEP 9: Disable Failure Simulation
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 9: Disable Failure Simulation"
echo -e "==========================================${NC}"
echo ""

call_api "POST" "/resilience/simulate/payment" '{"enable": false}' "Disabling payment failure simulation"

pause

# ============================================
# STEP 10: Successful Checkout
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 10: Successful Checkout"
echo -e "==========================================${NC}"
echo ""

SUCCESS_USER="success-user-$(date +%s)"
curl -s -X POST "$BASE_URL/cart/$SUCCESS_USER/items" \
    -H "Content-Type: application/json" \
    -d '{"product_id": 500, "quantity": 3}' > /dev/null

call_api "GET" "/cart/$SUCCESS_USER" "" "Viewing cart"
call_api "POST" "/cart/$SUCCESS_USER/checkout" "" "Processing checkout (should succeed)"

# ============================================
# STEP 11: Final Status
# ============================================
echo -e "${CYAN}=========================================="
echo "  Step 11: Final Circuit Breaker Status"
echo -e "==========================================${NC}"
echo ""

call_api "GET" "/resilience/circuit-breakers" "" "Final circuit breaker status"

# ============================================
# Summary
# ============================================
echo -e "${GREEN}=========================================="
echo "  Testing Complete!"
echo -e "==========================================${NC}"
echo ""
echo "What we demonstrated:"
echo "  ✅ Retry pattern with exponential backoff"
echo "  ✅ Circuit breaker opening after failures"
echo "  ✅ Fast-fail when circuit is open"
echo "  ✅ Manual circuit breaker reset"
echo "  ✅ Recovery after failures disabled"
echo ""
echo -e "${CYAN}View CloudWatch Logs:${NC}"
echo "  aws logs tail /ecs/$(terraform output -raw ecs_service_name 2>/dev/null || echo 'week6-experiment') --follow"
echo ""
echo -e "${CYAN}Monitor in AWS Console:${NC}"
echo "  https://console.aws.amazon.com/ecs"
echo ""

