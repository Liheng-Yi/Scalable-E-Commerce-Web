#!/bin/bash
# Quick script to manually restart ECS service

set -e

echo "=========================================="
echo "  ECS Service Restart"
echo "=========================================="
echo ""

# Get values from Terraform
CLUSTER=$(terraform output -raw ecs_cluster_name 2>/dev/null)
SERVICE=$(terraform output -raw ecs_service_name 2>/dev/null)
REGION=$(terraform output -raw aws_region 2>/dev/null)

if [ -z "$CLUSTER" ] || [ -z "$SERVICE" ] || [ -z "$REGION" ]; then
    echo "❌ Could not get service details from Terraform."
    echo "   Make sure you're in the terraform directory and have deployed."
    exit 1
fi

echo "Cluster: $CLUSTER"
echo "Service: $SERVICE"
echo "Region: $REGION"
echo ""

# Show current tasks
echo "Current running tasks:"
TASK_COUNT=$(aws ecs list-tasks \
    --cluster "$CLUSTER" \
    --service-name "$SERVICE" \
    --region "$REGION" \
    --query 'taskArns' \
    --output text | wc -w)

echo "  Running tasks: $TASK_COUNT"
echo ""

# Ask user which method
echo "Choose restart method:"
echo ""
echo "  1) Force new deployment (recommended - zero downtime)"
echo "  2) Stop all tasks (faster but brief downtime)"
echo "  3) Cancel"
echo ""
read -p "Enter choice (1-3): " choice

case $choice in
    1)
        echo ""
        echo "Forcing new deployment..."
        aws ecs update-service \
            --cluster "$CLUSTER" \
            --service "$SERVICE" \
            --force-new-deployment \
            --region "$REGION" \
            > /dev/null
        
        echo "✅ New deployment started!"
        echo ""
        echo "Tasks will restart gracefully (zero downtime)."
        echo "This may take 1-2 minutes."
        echo ""
        echo "Watch progress:"
        echo "  aws ecs describe-services --cluster $CLUSTER --services $SERVICE --region $REGION --query 'services[0].events[0:5]'"
        ;;
        
    2)
        echo ""
        echo "⚠️  WARNING: This will cause brief service downtime!"
        read -p "Are you sure? (yes/no): " confirm
        
        if [ "$confirm" != "yes" ]; then
            echo "Cancelled."
            exit 0
        fi
        
        echo ""
        echo "Listing tasks to stop..."
        TASKS=$(aws ecs list-tasks \
            --cluster "$CLUSTER" \
            --service-name "$SERVICE" \
            --region "$REGION" \
            --query 'taskArns[]' \
            --output text)
        
        if [ -z "$TASKS" ]; then
            echo "No tasks running."
            exit 0
        fi
        
        echo "Stopping tasks..."
        for TASK in $TASKS; do
            echo "  Stopping: $(basename $TASK)"
            aws ecs stop-task \
                --cluster "$CLUSTER" \
                --task "$TASK" \
                --reason "Manual restart via script" \
                --region "$REGION" \
                > /dev/null
        done
        
        echo ""
        echo "✅ Tasks stopped! ECS will automatically start new ones."
        echo ""
        echo "Wait 30 seconds for new tasks to start..."
        sleep 5
        
        NEW_COUNT=$(aws ecs list-tasks \
            --cluster "$CLUSTER" \
            --service-name "$SERVICE" \
            --region "$REGION" \
            --query 'taskArns' \
            --output text | wc -w)
        
        echo "New running tasks: $NEW_COUNT"
        ;;
        
    3)
        echo "Cancelled."
        exit 0
        ;;
        
    *)
        echo "Invalid choice."
        exit 1
        ;;
esac

echo ""
echo "=========================================="
echo "  Restart Complete!"
echo "=========================================="
echo ""

