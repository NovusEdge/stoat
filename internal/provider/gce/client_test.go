package gce

import (
	"context"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/settings"
)

func TestClientOptionsPlainADC(t *testing.T) {
	got, err := clientOptions(context.Background(), settings.GCE{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("clientOptions = %d options, want none: plain ADC needs no option", len(got))
	}
}

func TestClientOptionsRejectsBothCredentialSources(t *testing.T) {
	_, err := validateCredentials(settings.GCE{
		ImpersonateServiceAccount: "a@b.iam.gserviceaccount.com",
		ServiceAccountKeyFile:     "/tmp/key.json",
	})
	if err == nil {
		t.Fatal("validateCredentials() = nil, want an error naming both keys")
	}
	if !strings.Contains(err.Error(), "impersonate_service_account") ||
		!strings.Contains(err.Error(), "service_account_key_file") {
		t.Errorf("error %q must name both config keys", err)
	}
}
