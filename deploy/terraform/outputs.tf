output "web_url" {
  value = "https://${local.web_host}"
}

output "ssh_host" {
  value = local.ssh_host
}

output "tunnel_id" {
  value = cloudflare_zero_trust_tunnel_cloudflared.this.id
}

# Show with: terraform output -raw tunnel_token
# Then on the mini PC (admin PowerShell): cloudflared service install <token>
output "tunnel_token" {
  value     = data.cloudflare_zero_trust_tunnel_cloudflared_token.this.token
  sensitive = true
}
