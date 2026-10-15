locals {
  count = var.enable_deployment ? 1 : 0
  tags  = merge({ Project = var.name, ManagedBy = "Terraform" }, var.tags)
}

resource "aws_vpc" "this" {
  count                = local.count
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags                 = merge(local.tags, { Name = var.name })
}

resource "aws_internet_gateway" "this" {
  count  = local.count
  vpc_id = aws_vpc.this[0].id
  tags   = merge(local.tags, { Name = "${var.name}-igw" })
}

resource "aws_subnet" "public" {
  count                   = var.enable_deployment ? 2 : 0
  vpc_id                  = aws_vpc.this[0].id
  availability_zone       = var.availability_zones[count.index]
  cidr_block              = cidrsubnet(var.vpc_cidr, 4, count.index)
  map_public_ip_on_launch = true
  tags                    = merge(local.tags, { Name = "${var.name}-public-${count.index + 1}" })
}

resource "aws_subnet" "private" {
  count             = var.enable_deployment ? 2 : 0
  vpc_id            = aws_vpc.this[0].id
  availability_zone = var.availability_zones[count.index]
  cidr_block        = cidrsubnet(var.vpc_cidr, 4, count.index + 8)
  tags              = merge(local.tags, { Name = "${var.name}-private-${count.index + 1}" })
}

resource "aws_route_table" "public" {
  count  = local.count
  vpc_id = aws_vpc.this[0].id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this[0].id
  }
  tags = merge(local.tags, { Name = "${var.name}-public" })
}

resource "aws_route_table_association" "public" {
  count          = var.enable_deployment ? 2 : 0
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public[0].id
}

resource "aws_security_group" "data" {
  count       = local.count
  name_prefix = "${var.name}-data-"
  description = "Data-plane access from ForgeGate workloads"
  vpc_id      = aws_vpc.this[0].id
  ingress {
    description = "PostgreSQL inside VPC"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }
  ingress {
    description = "Redis inside VPC"
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = local.tags
}

resource "aws_db_subnet_group" "this" {
  count      = local.count
  name       = var.name
  subnet_ids = aws_subnet.private[*].id
  tags       = local.tags
}

resource "aws_db_instance" "postgres" {
  count                        = local.count
  identifier                   = "${var.name}-postgres"
  engine                       = "postgres"
  engine_version               = "16"
  instance_class               = var.db_instance_class
  allocated_storage            = 20
  max_allocated_storage        = 100
  storage_encrypted            = true
  db_name                      = "forgegate"
  username                     = "forgegate"
  password                     = var.db_password
  db_subnet_group_name         = aws_db_subnet_group.this[0].name
  vpc_security_group_ids       = [aws_security_group.data[0].id]
  backup_retention_period      = 7
  deletion_protection          = true
  skip_final_snapshot          = false
  final_snapshot_identifier    = "${var.name}-final"
  auto_minor_version_upgrade   = true
  performance_insights_enabled = true
  tags                         = local.tags
}

resource "aws_elasticache_subnet_group" "this" {
  count      = local.count
  name       = var.name
  subnet_ids = aws_subnet.private[*].id
}

resource "aws_elasticache_replication_group" "redis" {
  count                      = local.count
  replication_group_id       = "${var.name}-redis"
  description                = "ForgeGate distributed rate limiting"
  node_type                  = "cache.t4g.micro"
  port                       = 6379
  subnet_group_name          = aws_elasticache_subnet_group.this[0].name
  security_group_ids         = [aws_security_group.data[0].id]
  at_rest_encryption_enabled = true
  transit_encryption_enabled = true
  automatic_failover_enabled = true
  multi_az_enabled           = true
  num_cache_clusters         = 2
  tags                       = local.tags
}

resource "aws_ecs_cluster" "this" {
  count = local.count
  name  = var.name
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
  tags = local.tags
}

resource "aws_ecr_repository" "gateway" {
  count                = local.count
  name                 = "${var.name}/gateway"
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
  tags = local.tags
}
resource "aws_ecr_repository" "worker" {
  count                = local.count
  name                 = "${var.name}/worker"
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
  tags = local.tags
}
