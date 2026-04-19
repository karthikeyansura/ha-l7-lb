output "instance_id" {
  description = "EC2 instance id for `aws ssm send-command --instance-ids`."
  value       = aws_instance.locust.id
}

output "public_ip" {
  description = "Public IP of the Locust EC2 (SSH not enabled; for reference only)."
  value       = aws_instance.locust.public_ip
}

output "s3_bucket" {
  description = "S3 bucket holding Locust artifacts."
  value       = aws_s3_bucket.results.id
}
