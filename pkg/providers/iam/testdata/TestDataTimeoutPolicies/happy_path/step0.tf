provider "akamai" {
  edgerc = "../../test/edgerc"
}

data "akamai_iam_timeout_policies" "test" {}
