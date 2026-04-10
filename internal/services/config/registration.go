// Package config implements VyOS configuration resources.
package config

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Resources returns the list of resources in this package.
func Resources() []func() resource.Resource {
	return []func() resource.Resource{
		NewConfigBlockTreeResource,
	}
}

// DataSources returns the list of data sources in this package.
func DataSources() []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}
