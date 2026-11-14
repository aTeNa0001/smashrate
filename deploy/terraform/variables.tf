variable "cloudflare_api_token" {
  description = "API token with Account > Cloudflare Tunnel: Edit, Access: Apps and Policies: Edit, Access: Organizations, Identity Providers, and Groups: Edit, Zone > DNS: Edit"
  type        = string
  sensitive   = true
}

variable "account_id" {
  description = "Cloudflare account id (dashboard URL: dash.cloudflare.com/<account_id>)"
  type        = string
}

variable "zone_id" {
  description = "Zone id of the domain (domain Overview page, right column)"
  type        = string
}

variable "zone_name" {
  description = "The domain, e.g. example.com"
  type        = string
}

variable "web_subdomain" {
  description = "Host for the web UI: smashrate -> smashrate.example.com"
  type        = string
  default     = "smashrate"
}

variable "ssh_subdomain" {
  description = "Host for SSH maintenance: ssh -> ssh.example.com"
  type        = string
  default     = "ssh"
}

variable "tunnel_name" {
  type    = string
  default = "smashrate"
}

variable "member_emails" {
  description = "Who may open the web UI (a One-time PIN is e-mailed to these addresses)"
  type        = list(string)
}

variable "admin_emails" {
  description = "Who may use SSH (usually just you)"
  type        = list(string)
}

variable "create_otp_idp" {
  description = "Create the One-time PIN identity provider. Set false when Zero Trust > Settings > Authentication already lists one."
  type        = bool
  default     = true
}

variable "session_duration" {
  description = "How long an Access login to the web UI lasts"
  type        = string
  default     = "720h" # 30 days
}
