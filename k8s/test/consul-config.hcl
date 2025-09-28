# Boulder services configuration for Consul in Kubernetes
# All services point to the boulder-monolith service

services {
  id      = "email-exporter-a"
  name    = "email-exporter"
  address = "boulder-monolith"
  port    = 9603
  tags    = ["tcp"]
}

services {
  id      = "boulder-a"
  name    = "boulder"
  address = "boulder-monolith"
}

services {
  id      = "ca-a"
  name    = "ca"
  address = "boulder-monolith"
  port    = 9393
  tags    = ["tcp"]
}

services {
  id      = "ca-b"
  name    = "ca"
  address = "boulder-monolith"
  port    = 9493
  tags    = ["tcp"]
}

services {
  id      = "crl-storer-a"
  name    = "crl-storer"
  address = "boulder-monolith"
  port    = 9309
  tags    = ["tcp"]
}

services {
  id      = "dns-a"
  name    = "dns"
  address = "boulder-monolith"
  port    = 8053
  tags    = ["udp"]
}

services {
  id      = "dns-b"
  name    = "dns"
  address = "boulder-monolith"
  port    = 8054
  tags    = ["udp"]
}

services {
  id      = "doh-a"
  name    = "doh"
  address = "boulder-monolith"
  port    = 8343
  tags    = ["tcp"]
}

services {
  id      = "doh-b"
  name    = "doh"
  address = "boulder-monolith"
  port    = 8443
  tags    = ["tcp"]
}

services {
  id      = "nonce-taro-a"
  name    = "nonce-taro"
  address = "boulder-monolith"
  port    = 9301
  tags    = ["tcp"]
}

services {
  id      = "nonce-taro-b"
  name    = "nonce-taro"
  address = "boulder-monolith"
  port    = 9501
  tags    = ["tcp"]
}

services {
  id      = "nonce-zinc"
  name    = "nonce-zinc"
  address = "boulder-monolith"
  port    = 9401
  tags    = ["tcp"]
}

services {
  id      = "publisher-a"
  name    = "publisher"
  address = "boulder-monolith"
  port    = 9391
  tags    = ["tcp"]
}

services {
  id      = "publisher-b"
  name    = "publisher"
  address = "boulder-monolith"
  port    = 9491
  tags    = ["tcp"]
}

services {
  id      = "ra-sct-provider-a"
  name    = "ra-sct-provider"
  address = "boulder-monolith"
  port    = 9594
  tags    = ["tcp"]
}

services {
  id      = "ra-sct-provider-b"
  name    = "ra-sct-provider"
  address = "boulder-monolith"
  port    = 9694
  tags    = ["tcp"]
}

services {
  id      = "ra-a"
  name    = "ra"
  address = "boulder-monolith"
  port    = 9394
  tags    = ["tcp"]
}

services {
  id      = "ra-b"
  name    = "ra"
  address = "boulder-monolith"
  port    = 9494
  tags    = ["tcp"]
}

services {
  id      = "rva1-a"
  name    = "rva1"
  address = "boulder-monolith"
  port    = 9397
  tags    = ["tcp"]
}

services {
  id      = "rva1-b"
  name    = "rva1"
  address = "boulder-monolith"
  port    = 9498
  tags    = ["tcp"]
}

services {
  id      = "rva1-c"
  name    = "rva1"
  address = "boulder-monolith"
  port    = 9499
  tags    = ["tcp"]
}

services {
  id      = "rva2-a"
  name    = "rva2"
  address = "boulder-monolith"
  port    = 9897
  tags    = ["tcp"]
}

services {
  id      = "rva2-b"
  name    = "rva2"
  address = "boulder-monolith"
  port    = 9998
  tags    = ["tcp"]
}

services {
  id      = "sa-a"
  name    = "sa"
  address = "boulder-monolith"
  port    = 9395
  tags    = ["tcp"]
}

services {
  id      = "sa-b"
  name    = "sa"
  address = "boulder-monolith"
  port    = 9495
  tags    = ["tcp"]
}

services {
  id      = "va-a"
  name    = "va"
  address = "boulder-monolith"
  port    = 9392
  tags    = ["tcp"]
}

services {
  id      = "va-b"
  name    = "va"
  address = "boulder-monolith"
  port    = 9492
  tags    = ["tcp"]
}

services {
  id      = "bredis3"
  name    = "redisratelimits"
  address = "bredis-1"
  port    = 6379
  tags    = ["tcp"]
}

services {
  id      = "bredis4"
  name    = "redisratelimits"
  address = "bredis-2"
  port    = 6379
  tags    = ["tcp"]
}