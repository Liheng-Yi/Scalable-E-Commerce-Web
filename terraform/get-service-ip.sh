#!/bin/bash
# Script to get the public IP of the ECS Fargate task

set -e

echo "Fetching ECS service details..."
echo ""

# Get Terraform outputs
CLUSTER_NAME=$(terraform output -raw ecs_cluster_name)
SERVICE_NAME=$(terraform output -raw ecs_service_name)
AWS_REGION=$(terraform output -raw aws_region)

echo "Cluster: $CLUSTER_NAME"
echo "Service: $SERVICE_NAME"
echo "Region: $AWS_REGION"
echo ""

# Get task ARN
echo "Finding running tasks..."
TASK_ARN=$(aws ecs list-tasks \
    --cluster "$CLUSTER_NAME" \
    --service-name "$SERVICE_NAME" \
    --region "$AWS_REGION" \
    --query 'taskArns[0]' \
    --output text)

if [ "$TASK_ARN" == "None" ] || [ -z "$TASK_ARN" ]; then
    echo "❌ No running tasks found!"
    echo ""
    echo "Check the service status:"
    echo "  aws ecs describe-services --cluster $CLUSTER_NAME --services $SERVICE_NAME --region $AWS_REGION"
    exit 1
fi

echo "✅ Task found: $TASK_ARN"
echo ""

# Get task details
echo "Fetching task details..."
TASK_DETAILS=$(aws ecs describe-tasks \
    --cluster "$CLUSTER_NAME" \
    --tasks "$TASK_ARN" \
    --region "$AWS_REGION")

# Extract ENI ID (using Python instead of jq for better compatibility)
ENI_ID=$(echo "$TASK_DETAILS" | python -c "
import sys, json
try:
    data = json.load(sys.stdin)
    for detail in data['tasks'][0]['attachments'][0]['details']:
        if detail['name'] == 'networkInterfaceId':
            print(detail['value'])
            break
except Exception as e:
    print('', file=sys.stderr)
" 2>/dev/null)

# Get public IP
echo "Fetching public IP..."
PUBLIC_IP=$(aws ec2 describe-network-interfaces \
    --network-interface-ids "$ENI_ID" \
    --region "$AWS_REGION" \
    --query 'NetworkInterfaces[0].Association.PublicIp' \
    --output text)

if [ -z "$PUBLIC_IP" ] || [ "$PUBLIC_IP" == "None" ]; then
    echo "❌ No public IP found! The task might still be starting up."
    echo "   Wait a minute and try again."
    exit 1
fi


echo "  🎉 Service is ready!"
echo "=========================================="
echo ""
echo "Public IP: $PUBLIC_IP"



