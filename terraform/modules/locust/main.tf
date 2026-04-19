# Locust load generator module.
#
# Provisions a single EC2 instance in the existing default VPC with:
#   - Amazon Linux 2023 AMI (SSM agent pre-installed)
#   - IAM instance profile for SSM Run Command + S3 read/write
#   - S3 bucket for experiment artifacts (Locust CSVs, run configs)
#   - User-data that installs locust + aws CLI and pulls the locustfile
#
# No SSH is exposed. All interaction is via `aws ssm send-command`,
# which authenticates through IAM — no key management needed.
#
# Run harness (outside terraform):
#   aws ssm send-command \
#     --instance-ids $(terraform output -raw locust_instance_id) \
#     --document-name AWS-RunShellScript \
#     --parameters commands='cd /opt/locust && locust --headless ...'
#
# Artifacts land in s3://ha-l7-lb-locust-results/<exp>/<run>/ and are
# pulled locally via `aws s3 sync`.

data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-x86_64"]
  }
  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
  filter {
    name   = "architecture"
    values = ["x86_64"]
  }
}

# Results bucket — short lifecycle so forgotten artifacts self-clean.
resource "aws_s3_bucket" "results" {
  bucket        = "${var.service_name}-locust-results"
  force_destroy = true
}

resource "aws_s3_bucket_lifecycle_configuration" "results" {
  bucket = aws_s3_bucket.results.id
  rule {
    id     = "expire-after-7-days"
    status = "Enabled"
    filter {}
    expiration { days = 7 }
  }
}

# Upload the locustfile on every apply so updates land on the EC2 at boot.
resource "aws_s3_object" "locustfile" {
  bucket = aws_s3_bucket.results.id
  key    = "bootstrap/locustfile.py"
  source = var.locustfile_path
  etag   = filemd5(var.locustfile_path)
}

# Instance profile: SSM for remote exec + S3 for pulling locustfile and
# pushing results.
resource "aws_iam_role" "locust" {
  name = "${var.service_name}-locust-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.locust.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy" "s3_results" {
  name = "${var.service_name}-locust-s3"
  role = aws_iam_role.locust.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:PutObject",
        "s3:GetObject",
        "s3:ListBucket",
      ]
      Resource = [
        aws_s3_bucket.results.arn,
        "${aws_s3_bucket.results.arn}/*",
      ]
    }]
  })
}

resource "aws_iam_instance_profile" "locust" {
  name = "${var.service_name}-locust-profile"
  role = aws_iam_role.locust.name
}

# SG: outbound all (SSM endpoints, NLB, S3, PyPI). Inbound: none.
resource "aws_security_group" "locust" {
  name        = "${var.service_name}-locust-sg"
  description = "Locust EC2 - egress only; SSM-only access."
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# Allow Locust EC2 to scrape the LB metrics endpoint (9080) directly.
# The NLB only forwards port 80 to LB:8080, so the metrics port is
# otherwise unreachable. This rule attaches to the LB SG rather than
# modifying the shared network module, keeping locust module self-contained.
resource "aws_security_group_rule" "lb_metrics_from_locust" {
  type                     = "ingress"
  from_port                = var.lb_metrics_port
  to_port                  = var.lb_metrics_port
  protocol                 = "tcp"
  source_security_group_id = aws_security_group.locust.id
  security_group_id        = var.lb_security_group_id
  description              = "Locust EC2 scraping LB /metrics"
}

# User-data: install python3-pip + awscli + locust, then pull the locustfile.
# The locustfile URL is baked into the rendered script so the instance can
# self-bootstrap without further intervention.
locals {
  user_data = <<-EOT
    #!/bin/bash
    set -uxo pipefail
    # AL2023 ships with RPM-owned pip/setuptools that cannot be uninstalled
    # in-place; locust needs a newer setuptools to build its deps. Install
    # into a dedicated venv so system packages are untouched.
    dnf install -y python3-pip gcc python3-devel awscli || true
    python3 -m venv /opt/locust-venv
    /opt/locust-venv/bin/pip install --upgrade pip setuptools wheel
    /opt/locust-venv/bin/pip install locust
    ln -sf /opt/locust-venv/bin/locust /usr/local/bin/locust
    mkdir -p /opt/locust
    for i in 1 2 3 4 5; do
      if aws s3 cp s3://${aws_s3_bucket.results.id}/bootstrap/locustfile.py /opt/locust/locustfile.py; then
        break
      fi
      sleep 5
    done
    chmod 755 /opt/locust
    echo "locust bootstrap complete at $(date)" >> /var/log/locust-bootstrap.log
  EOT
}

resource "aws_instance" "locust" {
  ami                         = data.aws_ami.al2023.id
  instance_type               = var.instance_type
  subnet_id                   = var.subnet_id
  vpc_security_group_ids      = [aws_security_group.locust.id]
  iam_instance_profile        = aws_iam_instance_profile.locust.name
  associate_public_ip_address = true
  user_data                   = local.user_data

  # Replace the instance if user_data changes (needed when locustfile evolves).
  user_data_replace_on_change = true

  tags = {
    Name = "${var.service_name}-locust"
    Role = "load-generator"
  }

  # Ensure the S3 object exists before the EC2 tries to pull it.
  depends_on = [aws_s3_object.locustfile]
}
