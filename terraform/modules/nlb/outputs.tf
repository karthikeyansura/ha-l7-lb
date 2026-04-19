output "nlb_dns_name" { value = aws_lb.this.dns_name }
output "target_group_arn" { value = aws_lb_target_group.this.arn }
output "nlb_arn_suffix" { value = aws_lb.this.arn_suffix }
