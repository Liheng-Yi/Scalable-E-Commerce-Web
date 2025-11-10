#!/bin/bash
# Script to destroy all AWS resources created by Terraform

set -e

echo "=========================================="
echo "  AWS Resource Cleanup"
echo "=========================================="
echo ""
echo "⚠️  WARNING: This will destroy all AWS resources created by Terraform!"
echo ""
echo "Resources to be destroyed:"
echo "  - ECS Cluster and Service"
echo "  - ECR Repository (including all Docker images)"
echo "  - CloudWatch Log Group"
echo "  - Security Group"
echo ""

read -p "Are you sure you want to destroy all resources? (yes/no): " confirm
if [ "$confirm" != "yes" ]; then
    echo "Destruction cancelled."
    exit 0
fi

echo ""
echo "Destroying resources..."
terraform destroy

echo ""
echo "=========================================="
echo "  Cleanup Complete!"
echo "=========================================="
echo ""

