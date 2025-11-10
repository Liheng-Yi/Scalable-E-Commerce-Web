#!/bin/bash
# Quick script to get ALB URL for Locust testing

set -e

echo "=========================================="
echo "  Getting ALB URL for Load Testing"
echo "=========================================="
echo ""

# Get ALB URL
ALB_URL=$(terraform output -raw alb_dns_name)

if [ -z "$ALB_URL" ]; then
    echo "❌ Could not get ALB URL. Make sure you've deployed with Terraform first."
    echo ""
    echo "Run: ./deploy.sh"
    exit 1
fi

echo "✅ Your service is deployed!"
echo ""
echo "ALB URL: http://$ALB_URL"
echo ""
echo "=========================================="
echo "  Quick Tests"
echo "=========================================="
echo ""
echo "# Health check"
echo "curl http://$ALB_URL/health"
echo ""
echo "# Search (triggers memory leak - 10MB)"
echo "curl \"http://$ALB_URL/products/search?q=electronics\""
echo ""
echo "# Check memory status"
echo "curl http://$ALB_URL/memory"
echo ""
echo "=========================================="
echo "  Start Locust Load Testing"
echo "=========================================="
echo ""
echo "Run this command to start Locust:"
echo ""
echo "  cd .."
echo "  locust -f locustfile.py --host=http://$ALB_URL"
echo ""
echo "Then open: http://localhost:8089"
echo ""
echo "Recommended settings for crash demo:"
echo "  - Number of users: 30-50"
echo "  - Spawn rate: 10"
echo ""
echo "Watch it crash in ~2-3 minutes! 🔥"
echo ""

