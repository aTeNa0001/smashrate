locals {
  web_host = "${var.web_subdomain}.${var.zone_name}"
  ssh_host = "${var.ssh_subdomain}.${var.zone_name}"
}

# --- Tunnel. The mini PC only runs `cloudflared service install <token>`;
# the ingress rules below are stored on Cloudflare's side (remotely managed),
# so no config.yml is needed on the PC.

resource "cloudflare_zero_trust_tunnel_cloudflared" "this" {
  account_id = var.account_id
  name       = var.tunnel_name
  config_src = "cloudflare"
}

resource "cloudflare_zero_trust_tunnel_cloudflared_config" "this" {
  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
  config = {
    ingress = [
      {
        hostname = local.web_host
        service  = "http://localhost:8080"
      },
      {
        hostname = local.ssh_host
        service  = "ssh://localhost:22"
      },
      {
        service = "http_status:404"
      },
    ]
  }
}

data "cloudflare_zero_trust_tunnel_cloudflared_token" "this" {
  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
}

# --- DNS: both hosts point at the tunnel.

resource "cloudflare_dns_record" "web" {
  zone_id = var.zone_id
  name    = local.web_host
  type    = "CNAME"
  content = "${cloudflare_zero_trust_tunnel_cloudflared.this.id}.cfargotunnel.com"
  proxied = true
  ttl     = 1
}

resource "cloudflare_dns_record" "ssh" {
  zone_id = var.zone_id
  name    = local.ssh_host
  type    = "CNAME"
  content = "${cloudflare_zero_trust_tunnel_cloudflared.this.id}.cfargotunnel.com"
  proxied = true
  ttl     = 1
}

# --- Access: who may reach each host. Login is a One-time PIN e-mailed to
# the address, so nothing is registered beyond the lists in terraform.tfvars.

resource "cloudflare_zero_trust_access_identity_provider" "otp" {
  count      = var.create_otp_idp ? 1 : 0
  account_id = var.account_id
  name       = "One-time PIN"
  type       = "onetimepin"
  config     = {}
}

resource "cloudflare_zero_trust_access_policy" "members" {
  account_id = var.account_id
  name       = "${var.tunnel_name} members"
  decision   = "allow"
  include    = [for e in var.member_emails : { email = { email = e } }]
}

resource "cloudflare_zero_trust_access_policy" "admins" {
  account_id = var.account_id
  name       = "${var.tunnel_name} admins"
  decision   = "allow"
  include    = [for e in var.admin_emails : { email = { email = e } }]
}

resource "cloudflare_zero_trust_access_application" "web" {
  account_id       = var.account_id
  name             = "${var.tunnel_name} web"
  type             = "self_hosted"
  domain           = local.web_host
  session_duration = var.session_duration
  policies = [{
    id         = cloudflare_zero_trust_access_policy.members.id
    precedence = 1
  }]
}

resource "cloudflare_zero_trust_access_application" "ssh" {
  account_id       = var.account_id
  name             = "${var.tunnel_name} ssh"
  type             = "self_hosted"
  domain           = local.ssh_host
  session_duration = "24h"
  policies = [{
    id         = cloudflare_zero_trust_access_policy.admins.id
    precedence = 1
  }]
}
