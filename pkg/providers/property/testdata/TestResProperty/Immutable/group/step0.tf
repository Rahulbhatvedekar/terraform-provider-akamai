provider "akamai" {
  edgerc = "~/.edgerc"
}

resource "akamai_property" "test" {
  name = "test property"
  contract_id = "ctr_0"
  group       = "grp_0"
  product_id  = "prd_0"

  hostnames {
    cname_to= "to.test.domain"
    cname_from="from.test.domain"
    cert_provisioning_type= "DEFAULT"
  }

}
