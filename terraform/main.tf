# Wire together five focused modules: network, ecr, logging, dynamodb, ecs.

module "network" {
  source         = "./modules/network"
  service_name   = var.service_name
  container_port = var.container_port
}

module "ecr" {
  source          = "./modules/ecr"
  repository_name = var.ecr_repository_name
}

module "logging" {
  source            = "./modules/logging"
  service_name      = var.service_name
  retention_in_days = var.log_retention_days
}

module "dynamodb" {
  source       = "./modules/dynamodb"
  service_name = var.service_name
  environment  = var.environment
}

# Reuse an existing IAM role for ECS tasks
data "aws_iam_role" "lab_role" {
  name = "LabRole"
}

module "ecs" {
  source                 = "./modules/ecs"
  service_name           = var.service_name
  image                  = "${module.ecr.repository_url}:latest"
  container_port         = var.container_port
  subnet_ids             = module.network.subnet_ids
  security_group_ids     = [module.network.security_group_id]
  alb_security_group_id  = module.network.alb_security_group_id
  vpc_id                 = module.network.vpc_id
  execution_role_arn     = data.aws_iam_role.lab_role.arn
  task_role_arn          = data.aws_iam_role.lab_role.arn
  log_group_name         = module.logging.log_group_name
  region                 = var.aws_region
  
  # Auto Scaling Configuration
  min_capacity           = var.min_capacity
  max_capacity           = var.max_capacity
  cpu_target_value       = var.cpu_target_value
  memory_target_value    = var.memory_target_value
  scale_in_cooldown      = var.scale_in_cooldown
  scale_out_cooldown     = var.scale_out_cooldown
  
  # DynamoDB Table Names
  dynamodb_products_table = module.dynamodb.products_table_name
  dynamodb_carts_table    = module.dynamodb.carts_table_name
  dynamodb_orders_table   = module.dynamodb.orders_table_name
}

// Build & push the Go app image into ECR
resource "docker_image" "app" {
  name = "${module.ecr.repository_url}:latest"

  build {
    context = ".."
  }
}

resource "docker_registry_image" "app" {
  name = docker_image.app.name
}
