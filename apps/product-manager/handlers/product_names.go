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
	"kaspersky_endpoint_security": "Kaspersky Endpoint Security",
	"astra_linux_special_edition": "Astra Linux Special Edition",
	ksosFamily:                    "Kaspersky Small Office Security",
	drWebFamily:                   "Dr.Web Desktop Security Suite",
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
	return productName(key)
}

func evidenceProductName(key string, row evidence.Row) string {
	var details monitorDetails
	_ = json.Unmarshal([]byte(row.Details), &details)
	return observedProductName(key, monitorObservation{Row: row, details: details})
}
