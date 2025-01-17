package clientlists

import (
	"io/ioutil"
	"log"
	"os"
	"sync"
	"testing"

	"github.com/akamai/AkamaiOPEN-edgegrid-golang/v7/pkg/clientlists"
	"github.com/akamai/terraform-provider-akamai/v5/pkg/akamai"
	"github.com/akamai/terraform-provider-akamai/v5/pkg/common/testutils"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

var testAccProviders map[string]func() (*schema.Provider, error)

var testAccProvider *schema.Provider

func TestMain(m *testing.M) {
	testAccProvider = akamai.NewPluginProvider(NewSubprovider())()
	testAccProviders = map[string]func() (*schema.Provider, error){
		"akamai": func() (*schema.Provider, error) {
			return testAccProvider, nil
		},
	}
	if err := testutils.TFTestSetup(); err != nil {
		log.Fatal(err)
	}
	exitCode := m.Run()
	if err := testutils.TFTestTeardown(); err != nil {
		log.Fatal(err)
	}
	os.Exit(exitCode)
}

// Only allow one test at a time to patch the client via useClient()
var clientLock sync.Mutex

// useClient swaps out the client on the global instance for the duration of the given func
func useClient(client clientlists.ClientLists, f func()) {
	clientLock.Lock()
	orig := inst.client
	inst.client = client

	defer func() {
		inst.client = orig
		clientLock.Unlock()
	}()

	f()
}

// loadFixtureBytes returns the entire contents of the given file as a byte slice
func loadFixtureBytes(path string) []byte {
	contents, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return contents
}

// loadFixtureString returns the entire contents of the given file as a string
func loadFixtureString(path string) string {
	return string(loadFixtureBytes(path))
}
