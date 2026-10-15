output "deployment_enabled" {
  value = var.enable_deployment
}
output "vpc_id" {
  value = try(aws_vpc.this[0].id, null)
}
output "postgres_endpoint" {
  value     = try(aws_db_instance.postgres[0].address, null)
  sensitive = true
}
output "redis_endpoint" {
  value     = try(aws_elasticache_replication_group.redis[0].primary_endpoint_address, null)
  sensitive = true
}
output "ecs_cluster_name" {
  value = try(aws_ecs_cluster.this[0].name, null)
}
