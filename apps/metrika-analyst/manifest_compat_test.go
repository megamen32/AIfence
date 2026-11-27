package main

import "reflect"

// Older Airlock SDKs omit the ReadOnly manifest bit. Keep the contract check
// active when the field exists, while allowing those SDKs to compile.
func manifestReadOnly(definition any) (bool, bool) {
	v := reflect.ValueOf(definition)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return false, false
	}
	field := v.FieldByName("ReadOnly")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		return false, false
	}
	return field.Bool(), true
}
