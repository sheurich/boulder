client_addr = "0.0.0.0"
bind_addr   = "0.0.0.0"
log_level   = "ERROR"
datacenter  = "boulder-dc"

ui_config {
  enabled = true
}

ports {
  dns      = 53
  http     = 8500
  grpc     = 8502
}

services {
  id      = "consul-health"
  name    = "consul"
  address = "boulder-consul"
  port    = 8500
  tags    = ["http"]
}