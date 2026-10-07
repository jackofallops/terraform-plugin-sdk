// Copyright IBM Corp. 2019, 2026
// SPDX-License-Identifier: MPL-2.0

package schema

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-sdk/v2/internal/configs/hcl2shim"
)

func SerializeValueForHash(buf *bytes.Buffer, val interface{}, schema *Schema) {
	if val == nil {
		buf.WriteRune(';')
		return
	}

	if schema == nil {
		buf.WriteString(fmt.Sprint(val))
		buf.WriteRune(';')
		return
	}

	// Safe handling of UnknownVariableValue sentinel across all types
	if str, ok := val.(string); ok && str == hcl2shim.UnknownVariableValue {
		buf.WriteString("~unknown~;")
		return
	}

	switch schema.Type {
	case TypeBool:
		switch b := val.(type) {
		case bool:
			if b {
				buf.WriteRune('1')
			} else {
				buf.WriteRune('0')
			}
		case string:
			if b == "true" || b == "1" {
				buf.WriteRune('1')
			} else {
				buf.WriteRune('0')
			}
		case int:
			if b != 0 {
				buf.WriteRune('1')
			} else {
				buf.WriteRune('0')
			}
		default:
			buf.WriteRune('0')
		}
	case TypeInt:
		switch n := val.(type) {
		case int:
			buf.WriteString(strconv.Itoa(n))
		case int64:
			buf.WriteString(strconv.FormatInt(n, 10))
		case int32:
			buf.WriteString(strconv.Itoa(int(n)))
		case float64:
			buf.WriteString(strconv.FormatInt(int64(n), 10))
		case string:
			buf.WriteString(n)
		default:
			buf.WriteString(fmt.Sprint(val))
		}
	case TypeFloat:
		switch f := val.(type) {
		case float64:
			buf.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
		case float32:
			buf.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 64))
		case int:
			buf.WriteString(strconv.Itoa(f))
		case int64:
			buf.WriteString(strconv.FormatInt(f, 10))
		case string:
			buf.WriteString(f)
		default:
			buf.WriteString(fmt.Sprint(val))
		}
	case TypeString:
		switch s := val.(type) {
		case string:
			buf.WriteString(s)
		default:
			buf.WriteString(fmt.Sprint(val))
		}
	case TypeList:
		buf.WriteRune('(')
		switch l := val.(type) {
		case []interface{}:
			for _, innerVal := range l {
				serializeCollectionMemberForHash(buf, innerVal, schema.Elem)
			}
		case []string:
			for _, innerVal := range l {
				serializeCollectionMemberForHash(buf, innerVal, schema.Elem)
			}
		case *Set:
			if l != nil {
				for _, innerVal := range l.List() {
					serializeCollectionMemberForHash(buf, innerVal, schema.Elem)
				}
			}
		default:
			v := reflect.ValueOf(val)
			if v.IsValid() && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) {
				for i := 0; i < v.Len(); i++ {
					serializeCollectionMemberForHash(buf, v.Index(i).Interface(), schema.Elem)
				}
			} else {
				serializeCollectionMemberForHash(buf, val, schema.Elem)
			}
		}
		buf.WriteRune(')')
	case TypeMap:
		m, ok := val.(map[string]interface{})
		if !ok {
			if sm, ok := val.(map[string]string); ok {
				m = make(map[string]interface{}, len(sm))
				for k, v := range sm {
					m[k] = v
				}
			} else {
				buf.WriteString(fmt.Sprint(val))
				buf.WriteRune(';')
				return
			}
		}
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteRune('[')
		for _, k := range keys {
			innerVal := m[k]
			if innerVal == nil {
				continue
			}
			buf.WriteString(k)
			buf.WriteRune(':')

			switch innerVal := innerVal.(type) {
			case int:
				buf.WriteString(strconv.Itoa(innerVal))
			case int64:
				buf.WriteString(strconv.FormatInt(innerVal, 10))
			case float64:
				buf.WriteString(strconv.FormatFloat(innerVal, 'g', -1, 64))
			case string:
				buf.WriteString(innerVal)
			case bool:
				if innerVal {
					buf.WriteRune('1')
				} else {
					buf.WriteRune('0')
				}
			default:
				buf.WriteString(fmt.Sprint(innerVal))
			}

			buf.WriteRune(';')
		}
		buf.WriteRune(']')
	case TypeSet:
		buf.WriteRune('{')
		switch s := val.(type) {
		case *Set:
			if s != nil {
				for _, innerVal := range s.List() {
					serializeCollectionMemberForHash(buf, innerVal, schema.Elem)
				}
			}
		case []interface{}:
			for _, innerVal := range s {
				serializeCollectionMemberForHash(buf, innerVal, schema.Elem)
			}
		default:
			v := reflect.ValueOf(val)
			if v.IsValid() && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) {
				for i := 0; i < v.Len(); i++ {
					serializeCollectionMemberForHash(buf, v.Index(i).Interface(), schema.Elem)
				}
			} else {
				serializeCollectionMemberForHash(buf, val, schema.Elem)
			}
		}
		buf.WriteRune('}')
	default:
		buf.WriteString(fmt.Sprint(val))
	}
	buf.WriteRune(';')
}

// SerializeResourceForHash appends a serialization of the given resource config
// to the given buffer, guaranteeing deterministic results given the same value
// and schema.
//
// Its primary purpose is as input into a hashing function in order
// to hash complex substructures when used in sets, and so the serialization
// is not reversible.
func SerializeResourceForHash(buf *bytes.Buffer, val interface{}, resource *Resource) {
	if val == nil || resource == nil {
		return
	}
	m, ok := val.(map[string]interface{})
	if !ok {
		buf.WriteString(fmt.Sprint(val))
		return
	}
	sm := resource.SchemaMap()
	var keys []string
	allComputed := true
	for k, v := range sm {
		if v == nil {
			continue
		}
		if v.Optional || v.Required {
			allComputed = false
		}

		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		innerSchema := sm[k]
		if innerSchema == nil {
			continue
		}
		// Skip attributes that are not user-provided. Computed attributes
		// do not contribute to the hash since their ultimate value cannot
		// be known at plan/diff time.
		if !allComputed && !(innerSchema.Required || innerSchema.Optional) {
			continue
		}

		buf.WriteString(k)
		buf.WriteRune(':')
		innerVal := m[k]
		SerializeValueForHash(buf, innerVal, innerSchema)
	}
}

func serializeCollectionMemberForHash(buf *bytes.Buffer, val interface{}, elem interface{}) {
	switch tElem := elem.(type) {
	case *Schema:
		SerializeValueForHash(buf, val, tElem)
	case *Resource:
		buf.WriteRune('<')
		SerializeResourceForHash(buf, val, tElem)
		buf.WriteString(">;")
	default:
		buf.WriteString(fmt.Sprint(val))
	}
}
