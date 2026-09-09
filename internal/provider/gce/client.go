package gce

import (
	"context"
	"fmt"

	compute "cloud.google.com/go/compute/apiv1"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"

	"github.com/novusedge/stoat/internal/settings"
)

// computeScope is the one scope this provider needs. Impersonation mints a
// token source, and a token source must name its scopes; ADC on its own gets
// them from the client library.
const computeScope = "https://www.googleapis.com/auth/compute"

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
//
// Impersonation goes through the impersonate package rather than
// option.ImpersonateCredentials, which is deprecated. ADC stays the base
// credential either way: impersonate.CredentialsTokenSource signs with
// whatever ADC resolves and exchanges it for a token as the target account.
func clientOptions(ctx context.Context, s settings.GCE) ([]option.ClientOption, error) {
	var opts []option.ClientOption
	if s.ImpersonateServiceAccount != "" {
		ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: s.ImpersonateServiceAccount,
			Scopes:          []string{computeScope},
		})
		if err != nil {
			return nil, fmt.Errorf("impersonating %s: %w", s.ImpersonateServiceAccount, err)
		}
		opts = append(opts, option.WithTokenSource(ts))
	}
	if s.ServiceAccountKeyFile != "" {
		// Deprecated upstream because a key file on disk is a standing
		// credential someone can copy. That is the same reason d7 makes this
		// an explicit escape hatch behind its own config key rather than a
		// fallback, and there is no replacement for an environment that can
		// supply neither ADC nor impersonation.
		//nolint:staticcheck // SA1019: no replacement; see the comment above.
		opts = append(opts, option.WithCredentialsFile(s.ServiceAccountKeyFile))
	}
	return opts, nil
}

// newClient builds the Compute Engine instances client for s, authenticated
// with Application Default Credentials and, optionally, an impersonated
// service account or a key file.
func newClient(ctx context.Context, s settings.GCE) (*compute.InstancesClient, error) {
	s, err := validateCredentials(s)
	if err != nil {
		return nil, err
	}
	opts, err := clientOptions(ctx, s)
	if err != nil {
		return nil, err
	}
	return compute.NewInstancesRESTClient(ctx, opts...)
}

// newFirewallsClient authenticates the same way as newClient, for the
// separate Firewalls API surface insert and Destroy also need.
func newFirewallsClient(ctx context.Context, s settings.GCE) (*compute.FirewallsClient, error) {
	s, err := validateCredentials(s)
	if err != nil {
		return nil, err
	}
	opts, err := clientOptions(ctx, s)
	if err != nil {
		return nil, err
	}
	return compute.NewFirewallsRESTClient(ctx, opts...)
}
