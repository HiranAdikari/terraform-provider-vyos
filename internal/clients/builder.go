// Package clients wires up the VyOS API client for use by resources and data sources.
package clients

import "github.com/hiranadikari/terraform-provider-vyos/vyos"

// Build returns a configured VyOS API client.
func Build(endpoint, apiKey string, insecure bool) *vyos.Client {
	return vyos.NewClient(endpoint, apiKey, insecure)
}
