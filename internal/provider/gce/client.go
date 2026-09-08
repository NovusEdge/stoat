// Package gce implements the Provider interface over the Compute Engine API.
package gce

import (
	"context"
	"fmt"

	compute "cloud.google.com/go/compute/apiv1"
	"github.com/novusedge/stoat/internal/settings"
	"google.golang.org/api/option"
)

// validateCredentials rejects a config naming both an impersonated service
// account and a key file: nothing in the settings schema says which wins.
func validateCredentials(s settings.GCE) (settings.GCE, error) {
	if s.ImpersonateServiceAccount != "" && s.ServiceAccountKeyFile != "" {
		return s, fmt.Errorf("providers.gce: set only one of impersonate_service_account or service_account_key_file, not both")
	}
	return s, nil
}

// clientOptions builds the option.ClientOption list for s. Plain ADC needs
// none; it is the compute client's default.
func clientOptions(s settings.GCE) []option.ClientOption {
	var opts []option.ClientOption
	if s.ImpersonateServiceAccount != "" {
		opts = append(opts, option.ImpersonateCredentials(s.ImpersonateServiceAccount))
	}
	if s.ServiceAccountKeyFile != "" {
		opts = append(opts, option.WithCredentialsFile(s.ServiceAccountKeyFile))
	}
	return opts
}

// newClient builds the Compute Engine instances client for s, authenticated
// with Application Default Credentials and, optionally, an impersonated
// service account or a key file.
func newClient(ctx context.Context, s settings.GCE) (*compute.InstancesClient, error) {
	s, err := validateCredentials(s)
	if err != nil {
		return nil, err
	}
	return compute.NewInstancesRESTClient(ctx, clientOptions(s)...)
}
