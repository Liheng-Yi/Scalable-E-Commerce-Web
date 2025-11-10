#!/bin/bash
# Deployment script for AWS ECS deployment



# Initialize Terraform if needed
if [ ! -d ".terraform" ]; then
    echo "Initializing Terraform..."
    terraform init
    echo ""
fi

# Apply the plan

echo "Deploying to AWS..."
terraform apply


echo ""
echo "=========================================="
echo "  Deployment Complete!"
echo "=========================================="
echo ""

# Extract outputs
CLUSTER_NAME=$(terraform output -raw ecs_cluster_name)
SERVICE_NAME=$(terraform output -raw ecs_service_name)
AWS_REGION=$(terraform output -raw aws_region)

echo "To get the public IP of your service, run:"
echo ""
echo "  cd terraform && ./get-service-ip.sh"
echo ""
echo "Or manually via AWS Console:"
echo "  ECS → Clusters → $CLUSTER_NAME → Service → $SERVICE_NAME → Tasks → (click task) → Network section"
echo ""

