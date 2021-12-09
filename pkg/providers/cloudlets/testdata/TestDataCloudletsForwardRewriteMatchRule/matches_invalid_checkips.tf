provider "akamai" {
  edgerc = "~/.edgerc"
}

data "akamai_cloudlets_forward_rewrite_match_rule" "test" {
  match_rules {
    name = "rule1"
    matches {
      match_type     = "clientip"
      match_value    = "127.0.0.1"
      match_operator = "equals"
      check_ips      = "invalid"
      object_match_value {
        type  = "simple"
        value = ["fghi"]
      }
    }
    forward_settings {}
  }
}