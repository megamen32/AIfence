package handlers

import (
	"agent/internal/evidence"
	"encoding/json"
	"strings"
)

const (
	ksosFamily    = "kaspersky_small_office_security"
	drWebFamily   = "dr_web_desktop_security_suite"
	unknownFamily = "Семейство не установлено"
)

var catalogFamilyNames = map[string]string{
	"kaspersky_endpoint_security":             "Kaspersky Endpoint Security",
	"astra_linux_special_edition":             "Astra Linux Special Edition",
	ksosFamily:                                "Kaspersky Small Office Security",
	drWebFamily:                               "Dr.Web Desktop Security Suite",
	"microsoft_365_business_premium":          "Microsoft 365 Business Premium",
	"myoffice_private_cloud":                  "MyOffice Private Cloud",
	"red_hat_enterprise_linux_server_premium": "Red Hat Enterprise Linux Server Premium",
}

var catalogSKUFamily = map[string]string{
	"KL4141RCEFR":       ksosFamily,
	"LBW-BC-12M-100-A3": drWebFamily,
}

// Only the two roles of the old fixture pair have a catalog migration.
var legacyFixtureFamilies = map[string]string{"target": ksosFamily, "control": drWebFamily}

func legacyFixtureFamily(key string) string {
	if namespace, role, ok := strings.Cut(key, "_"); ok && namespace == "demo" {
		return legacyFixtureFamilies[role]
	}
	return ""
}

func productName(key string) string {
	if legacyFixtureFamily(key) != "" {
		return unknownFamily
	}
	if name := catalogFamilyNames[key]; name != "" {
		return name
	}
	return strings.ReplaceAll(key, "_", " ")
}

func educationalObservation(row evidence.Row, d monitorDetails) bool {
	return d.Synthetic || d.Origin == "synthetic_fixture" ||
		row.Ref.SourceID == "synthetic" || strings.HasPrefix(row.Ref.SourceID, "synthetic/")
}

func observedProductName(key string, row monitorObservation) string {
	if family := legacyFixtureFamily(key); family != "" && educationalObservation(row.Row, row.details) {
		return productName(family)
	}
	if family := watchdogTargetFamily(row); family != "" {
		return productName(family)
	}
	return productName(key)
}

// watchdogTargetFamily recovers the measured family from the producer's own
// source locators. A watchdog alert carries a store article in product_key,
// so the article alone would reach the director as a product name. The target
// family is read only from locators of the target series, never from the
// control series, and never invented.
func watchdogTargetFamily(row monitorObservation) string {
	control := row.details.Control
	if control == "" {
		return ""
	}
	var refs struct {
		Refs []struct {
			Ref evidence.SourceRef `json:"source_ref"`
		} `json:"source_refs"`
	}
	if json.Unmarshal([]byte(row.Details), &refs) != nil {
		return ""
	}
	for _, ref := range refs.Refs {
		for _, part := range strings.Split(ref.Ref.Locator, ";") {
			value, ok := strings.CutPrefix(part, "family=")
			if ok && value != "" && value != control {
				return value
			}
		}
	}
	return ""
}

func evidenceProductName(key string, row evidence.Row) string {
	var details monitorDetails
	_ = json.Unmarshal([]byte(row.Details), &details)
	return observedProductName(key, monitorObservation{Row: row, details: details})
}
