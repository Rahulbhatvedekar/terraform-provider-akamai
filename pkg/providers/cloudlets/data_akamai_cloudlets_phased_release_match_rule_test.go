package cloudlets

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
)

func TestDataCloudletsPhasedReleaseMatchRule(t *testing.T) {

	tests := map[string]struct {
		configPath       string
		expectedJSONPath string
		matchRulesSize   int
	}{
		"basic valid rule set": {
			configPath:       "testdata/TestDataCloudletsPhasedReleaseMatchRule/basic.tf",
			expectedJSONPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/rules/basic_rules.json",
			matchRulesSize:   1,
		},
		"match criteria PR - ObjectMatchValue of Object type": {
			configPath:       "testdata/TestDataCloudletsPhasedReleaseMatchRule/omv_object.tf",
			expectedJSONPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/rules/omv_object_rules.json",
			matchRulesSize:   2,
		},
		"match criteria PR - ObjectMatchValue of Simple type": {
			configPath:       "testdata/TestDataCloudletsPhasedReleaseMatchRule/omv_simple.tf",
			expectedJSONPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/rules/omv_simple_rules.json",
			matchRulesSize:   2,
		},
		"match criteria PR - without ObjectMatchValue": {
			configPath:       "testdata/TestDataCloudletsPhasedReleaseMatchRule/omv_empty.tf",
			expectedJSONPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/rules/omv_empty_rules.json",
			matchRulesSize:   2,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					{
						Config: loadFixtureString(test.configPath),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(
								"data.akamai_cloudlets_phased_release_match_rule.test", "json",
								loadFixtureString(test.expectedJSONPath)),
							resource.TestCheckResourceAttr(
								"data.akamai_cloudlets_phased_release_match_rule.test", "match_rules.0.type", "cdMatchRule"),
							resource.TestCheckResourceAttr(
								"data.akamai_cloudlets_phased_release_match_rule.test", "match_rules.#", strconv.Itoa(test.matchRulesSize)),
						),
					},
				},
			})
		})
	}
}

func TestIncorrectDataPhasedReleaseDeploymentMatchRule(t *testing.T) {
	tests := map[string]struct {
		configPath string
		withError  string
	}{
		"match criteria PR - missed type field in ObjectMatchValue": {
			configPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/omv_missed_type.tf",
			withError:  "Missing required argument",
		},
		"match criteria PR - invalid type value for ObjectMatchValue": {
			configPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/omv_invalid_type.tf",
			withError:  `expected type to be one of \[simple object\], got invalid_type`,
		},
		"match criteria PR - invalid percent value for forward_settings": {
			configPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/invalid_percent.tf",
			withError:  `expected percent to be in the range \(1 - 100\), got 123`,
		},
		"match criteria PR - invalid chack_ips value for matches": {
			configPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/matches_invalid_checkips.tf",
			withError:  `expected check_ips to be one of \[CONNECTING_IP XFF_HEADERS CONNECTING_IP XFF_HEADERS \], got invalid`,
		},
		"match criteria PR - invalid match_operator value matches": {
			configPath: "testdata/TestDataCloudletsPhasedReleaseMatchRule/matches_invalid_operator.tf",
			withError:  `expected match_operator to be one of \[contains exists equals \], got invalid`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					{
						Config: loadFixtureString(test.configPath),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(
								"data.akamai_cloudlets_phased_release_match_rule.test", "match_rules.0.type", "cdMatchRule"),
						),
						ExpectError: regexp.MustCompile(test.withError),
					},
				},
			})
		})
	}
}
