// Copyright IBM Corp. 2019, 2026
// SPDX-License-Identifier: MPL-2.0

package schema

import (
	"bytes"
	"testing"
)

func TestSerializeForHash(t *testing.T) {
	type testCase struct {
		Schema   interface{}
		Value    interface{}
		Expected string
	}

	tests := []testCase{
		{
			Schema: &Schema{
				Type: TypeInt,
			},
			Value:    0,
			Expected: "0;",
		},

		{
			Schema: &Schema{
				Type: TypeInt,
			},
			Value:    200,
			Expected: "200;",
		},

		{
			Schema: &Schema{
				Type: TypeBool,
			},
			Value:    true,
			Expected: "1;",
		},

		{
			Schema: &Schema{
				Type: TypeBool,
			},
			Value:    false,
			Expected: "0;",
		},

		{
			Schema: &Schema{
				Type: TypeFloat,
			},
			Value:    1.0,
			Expected: "1;",
		},

		{
			Schema: &Schema{
				Type: TypeFloat,
			},
			Value:    1.54,
			Expected: "1.54;",
		},

		{
			Schema: &Schema{
				Type: TypeFloat,
			},
			Value:    0.1,
			Expected: "0.1;",
		},

		{
			Schema: &Schema{
				Type: TypeString,
			},
			Value:    "hello",
			Expected: "hello;",
		},

		{
			Schema: &Schema{
				Type: TypeString,
			},
			Value:    "1",
			Expected: "1;",
		},

		{
			Schema: &Schema{
				Type: TypeList,
				Elem: &Schema{
					Type: TypeString,
				},
			},
			Value:    []interface{}{},
			Expected: "();",
		},

		{
			Schema: &Schema{
				Type: TypeList,
				Elem: &Schema{
					Type: TypeString,
				},
			},
			Value:    []interface{}{"hello", "world"},
			Expected: "(hello;world;);",
		},

		{
			Schema: &Schema{
				Type: TypeList,
				Elem: &Resource{
					Schema: map[string]*Schema{
						"fo": {
							Type:     TypeString,
							Required: true,
						},
						"fum": {
							Type:     TypeString,
							Required: true,
						},
					},
				},
			},
			Value: []interface{}{
				map[string]interface{}{
					"fo": "bar",
				},
				map[string]interface{}{
					"fo":  "baz",
					"fum": "boz",
				},
			},
			Expected: "(<fo:bar;fum:;>;<fo:baz;fum:boz;>;);",
		},

		{
			Schema: &Schema{
				Type: TypeSet,
				Elem: &Schema{
					Type: TypeString,
				},
			},
			Value: NewSet(func(i interface{}) int { return len(i.(string)) }, []interface{}{
				"hello",
				"woo",
			}),
			Expected: "{woo;hello;};",
		},

		{
			Schema: &Schema{
				Type: TypeMap,
				Elem: &Schema{
					Type: TypeString,
				},
			},
			Value: map[string]interface{}{
				"foo": "bar",
				"baz": "foo",
			},
			Expected: "[baz:foo;foo:bar;];",
		},

		{
			Schema: &Resource{
				Schema: map[string]*Schema{
					"name": {
						Type:     TypeString,
						Required: true,
					},
					"size": {
						Type:     TypeInt,
						Optional: true,
					},
					"green": {
						Type:     TypeBool,
						Optional: true,
						Computed: true,
					},
					"upside_down": {
						Type:     TypeBool,
						Computed: true,
					},
				},
			},
			Value: map[string]interface{}{
				"name":  "my-fun-database",
				"size":  12,
				"green": true,
			},
			Expected: "green:1;name:my-fun-database;size:12;",
		},

		// test TypeMap nested in Schema: GH-7091
		{
			Schema: &Resource{
				Schema: map[string]*Schema{
					"outer": {
						Type:     TypeSet,
						Required: true,
						Elem: &Schema{
							Type:     TypeMap,
							Optional: true,
						},
					},
				},
			},
			Value: map[string]interface{}{
				"outer": NewSet(func(i interface{}) int { return 42 }, []interface{}{
					map[string]interface{}{
						"foo": "bar",
						"baz": "foo",
					},
				}),
			},
			Expected: "outer:{[baz:foo;foo:bar;];};",
		},

		{
			Schema: &Resource{
				Schema: map[string]*Schema{
					"attr1": {
						Type:     TypeString,
						Computed: true,
					},
					"attr2": {
						Type:     TypeString,
						Computed: true,
					},
				},
			},
			Value: map[string]interface{}{
				"attr1": "value1",
				"attr2": "value2",
			},
			Expected: "attr1:value1;attr2:value2;",
		},
	}

	for _, test := range tests {
		var gotBuf bytes.Buffer
		schema := test.Schema

		switch s := schema.(type) {
		case *Schema:
			SerializeValueForHash(&gotBuf, test.Value, s)
		case *Resource:
			SerializeResourceForHash(&gotBuf, test.Value, s)
		}

		got := gotBuf.String()
		if got != test.Expected {
			t.Errorf("hash(%#v) got %#v, but want %#v", test.Value, got, test.Expected)
		}
	}
}

func TestSerializeForHash_Hardened(t *testing.T) {
	t.Parallel()

	// 1. Unknown sentinel values across different schema types
	sentinel := "74D93920-ED26-11E3-AC10-0800200C9A66"
	types := []*Schema{
		{Type: TypeInt},
		{Type: TypeBool},
		{Type: TypeFloat},
		{Type: TypeList, Elem: &Schema{Type: TypeString}},
		{Type: TypeSet, Elem: &Schema{Type: TypeString}},
		{Type: TypeMap, Elem: &Schema{Type: TypeString}},
	}
	for _, s := range types {
		var buf bytes.Buffer
		SerializeValueForHash(&buf, sentinel, s)
		if buf.String() != "~unknown~;" {
			t.Errorf("expected ~unknown~; for type %v, got %s", s.Type, buf.String())
		}
	}

	// 2. TypeInt with int64, float64, string
	var bufInt bytes.Buffer
	SerializeValueForHash(&bufInt, int64(1234567890123), &Schema{Type: TypeInt})
	if bufInt.String() != "1234567890123;" {
		t.Errorf("unexpected int64 serialization: %s", bufInt.String())
	}

	// 3. TypeBool with string representation
	var bufBool bytes.Buffer
	SerializeValueForHash(&bufBool, "true", &Schema{Type: TypeBool})
	if bufBool.String() != "1;" {
		t.Errorf("unexpected bool string serialization: %s", bufBool.String())
	}

	// 4. TypeSet with []interface{} instead of *Set
	var bufSet bytes.Buffer
	SerializeValueForHash(&bufSet, []interface{}{"item1"}, &Schema{Type: TypeSet, Elem: &Schema{Type: TypeString}})
	if bufSet.String() != "{item1;};" {
		t.Errorf("unexpected set slice serialization: %s", bufSet.String())
	}

	// 5. SerializeResourceForHash with non-map or nil value
	var bufRes bytes.Buffer
	res := &Resource{
		Schema: map[string]*Schema{
			"field": {Type: TypeString, Optional: true},
		},
	}
	SerializeResourceForHash(&bufRes, nil, res)
	if bufRes.Len() != 0 {
		t.Errorf("expected empty buffer for nil resource value")
	}

	bufRes.Reset()
	SerializeResourceForHash(&bufRes, "not-a-map", res)
	if bufRes.String() != "not-a-map" {
		t.Errorf("expected not-a-map, got %s", bufRes.String())
	}

	// 6. SerializeResourceForHash with missing keys or nil value in map
	bufRes.Reset()
	SerializeResourceForHash(&bufRes, map[string]interface{}{"field": nil}, res)
	if bufRes.String() != "field:;" {
		t.Errorf("expected field:;, got %s", bufRes.String())
	}
}
