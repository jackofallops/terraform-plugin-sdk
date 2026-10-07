package schema

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/go-cty/cty"
)

func TestSchema_CanonicaliseList_SortKeys(t *testing.T) {
	s := &Schema{
		Type:     TypeList,
		SortKeys: []string{"direction", "priority"},
	}

	input := []interface{}{
		map[string]interface{}{
			"direction": "Outbound",
			"priority":  200,
			"name":      "rule-out-200",
		},
		map[string]interface{}{
			"direction": "Inbound",
			"priority":  150,
			"name":      "rule-in-150",
		},
		map[string]interface{}{
			"direction": "Inbound",
			"priority":  100,
			"name":      "rule-in-100",
		},
	}

	sorted := s.canonicalizeList(input)

	expectedNames := []string{"rule-in-100", "rule-in-150", "rule-out-200"}
	actualNames := make([]string, len(sorted))
	for i, item := range sorted {
		actualNames[i] = item.(map[string]interface{})["name"].(string)
	}

	if !reflect.DeepEqual(actualNames, expectedNames) {
		t.Fatalf("expected order %v, got %v", expectedNames, actualNames)
	}
}

func TestSchema_CanonicaliseList_SortFunc(t *testing.T) {
	s := &Schema{
		Type: TypeList,
		SortFunc: func(a, b interface{}) bool {
			// Sort descending by priority
			mapA := a.(map[string]interface{})
			mapB := b.(map[string]interface{})
			return mapA["priority"].(int) > mapB["priority"].(int)
		},
	}

	input := []interface{}{
		map[string]interface{}{"priority": 100, "name": "low"},
		map[string]interface{}{"priority": 500, "name": "high"},
		map[string]interface{}{"priority": 200, "name": "medium"},
	}

	sorted := s.canonicalizeList(input)

	expectedNames := []string{"high", "medium", "low"}
	actualNames := make([]string, len(sorted))
	for i, item := range sorted {
		actualNames[i] = item.(map[string]interface{})["name"].(string)
	}

	if !reflect.DeepEqual(actualNames, expectedNames) {
		t.Fatalf("expected order %v, got %v", expectedNames, actualNames)
	}
}

func TestSchema_CanonicaliseList_Primitives(t *testing.T) {
	s := &Schema{
		Type:     TypeList,
		SortKeys: []string{"val"},
	}

	input := []interface{}{
		map[string]interface{}{"val": 100},
		map[string]interface{}{"val": 20},
		map[string]interface{}{"val": 5},
	}

	sorted := s.canonicalizeList(input)
	actualVals := []int{
		sorted[0].(map[string]interface{})["val"].(int),
		sorted[1].(map[string]interface{})["val"].(int),
		sorted[2].(map[string]interface{})["val"].(int),
	}
	expectedVals := []int{5, 20, 100}

	if !reflect.DeepEqual(actualVals, expectedVals) {
		t.Fatalf("expected numeric order %v, got %v", expectedVals, actualVals)
	}
}

func TestSchema_CanonicaliseListWithPermutation(t *testing.T) {
	s := &Schema{
		Type:     TypeList,
		SortKeys: []string{"id"},
	}

	input := []interface{}{
		map[string]interface{}{"id": "c"},
		map[string]interface{}{"id": "a"},
		map[string]interface{}{"id": "b"},
	}

	sorted, perm := s.canonicalizeListWithPermutation(input)
	expectedPerm := []int{1, 2, 0}
	if !reflect.DeepEqual(perm, expectedPerm) {
		t.Fatalf("expected perm %v, got %v", expectedPerm, perm)
	}
	expectedIDs := []string{"a", "b", "c"}
	for i, item := range sorted {
		if id := item.(map[string]interface{})["id"].(string); id != expectedIDs[i] {
			t.Fatalf("at index %d: expected %s, got %s", i, expectedIDs[i], id)
		}
	}
}

func TestResourceData_TypeList_SortKeys_SetAndGet(t *testing.T) {
	// ensuring this doesn't panic if acceptance test flag is set since this is a negative test
	t.Setenv("TF_ACC", "")
	s := map[string]*Schema{
		"rules": {
			Type:     TypeList,
			Optional: true,
			SortKeys: []string{"priority"},
			Elem: &Resource{
				Schema: map[string]*Schema{
					"priority": {Type: TypeInt, Required: true},
					"name":     {Type: TypeString, Required: true},
				},
			},
		},
	}

	d, err := schemaMap(s).Data(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error creating ResourceData: %s", err)
	}

	// Set in unsorted order
	err = d.Set("rules", []interface{}{
		map[string]interface{}{"priority": 200, "name": "rule2"},
		map[string]interface{}{"priority": 50, "name": "rule0"},
		map[string]interface{}{"priority": 100, "name": "rule1"},
	})
	if err != nil {
		t.Fatalf("unexpected error setting rules: %s", err)
	}

	// d.Get("rules") should return sorted by priority: rule0 (50), rule1 (100), rule2 (200)
	raw := d.Get("rules").([]interface{})
	if len(raw) != 3 {
		t.Fatalf("expected 3 items, got %d", len(raw))
	}
	expectedNames := []string{"rule0", "rule1", "rule2"}
	for i, name := range expectedNames {
		item := raw[i].(map[string]interface{})
		if item["name"] != name {
			t.Fatalf("at index %d: expected %s, got %v", i, name, item["name"])
		}
	}

	// Sub-key queries should address the sorted indices
	if n := d.Get("rules.0.name"); n != "rule0" {
		t.Fatalf("expected rules.0.name to be 'rule0', got %v", n)
	}
	if n := d.Get("rules.1.name"); n != "rule1" {
		t.Fatalf("expected rules.1.name to be 'rule1', got %v", n)
	}
	if n := d.Get("rules.2.name"); n != "rule2" {
		t.Fatalf("expected rules.2.name to be 'rule2', got %v", n)
	}

	// Setting duplicate keys must be rejected with error
	err = d.Set("rules", []interface{}{
		map[string]interface{}{"priority": 100, "name": "rule1"},
		map[string]interface{}{"priority": 100, "name": "rule-dup"},
	})
	if err == nil {
		t.Fatal("expected error when setting duplicate compound keys, got nil")
	}
}

func TestValidateSortKeysUniqueness(t *testing.T) {
	keys := []string{"direction", "priority"}

	// Unique items should pass
	uniqueList := []interface{}{
		map[string]interface{}{"direction": "Inbound", "priority": 100},
		map[string]interface{}{"direction": "Outbound", "priority": 100},
		map[string]interface{}{"direction": "Inbound", "priority": 200},
	}
	if err := validateSortKeysUniqueness(uniqueList, keys); err != nil {
		t.Fatalf("unexpected error on unique list: %s", err)
	}

	// Duplicate items should fail
	dupList := []interface{}{
		map[string]interface{}{"direction": "Inbound", "priority": 100, "name": "a"},
		map[string]interface{}{"direction": "Outbound", "priority": 200, "name": "b"},
		map[string]interface{}{"direction": "Inbound", "priority": 100, "name": "c"},
	}
	err := validateSortKeysUniqueness(dupList, keys)
	if err == nil {
		t.Fatal("expected error on duplicate items, got nil")
	}
	expectedSub := "[direction=Inbound, priority=100]"
	if !strings.Contains(err.Error(), expectedSub) {
		t.Fatalf("expected error containing %q, got: %s", expectedSub, err)
	}
}

func TestSchema_CanonicaliseList_AnchorAndComputedSecondaryKey(t *testing.T) {
	s := &Schema{
		Type:     TypeList,
		SortKeys: []string{"name", "id"},
	}

	input := []interface{}{
		map[string]interface{}{"name": "rule-beta", "id": "id-002"},
		map[string]interface{}{"name": "rule-alpha", "id": ""}, // unassigned id at create
		map[string]interface{}{"name": "rule-alpha", "id": "id-001"},
	}

	sorted := s.canonicalizeList(input)

	// rule-alpha with assigned id-001 should sort before unassigned rule-alpha,
	// and rule-beta should sort last due to anchor name.
	expected := []struct {
		name string
		id   string
	}{
		{"rule-alpha", "id-001"},
		{"rule-alpha", ""},
		{"rule-beta", "id-002"},
	}

	for i, exp := range expected {
		m := sorted[i].(map[string]interface{})
		if m["name"] != exp.name || m["id"] != exp.id {
			t.Fatalf("at index %d: expected name=%q id=%q, got name=%q id=%q", i, exp.name, exp.id, m["name"], m["id"])
		}
	}
}

func TestRealignProposedNewStateForSortKeys(t *testing.T) {
	sch := map[string]*Schema{
		"http_listener": {
			Type:     TypeList,
			Optional: true,
			SortKeys: []string{"name"},
			Elem: &Resource{
				Schema: map[string]*Schema{
					"name": {Type: TypeString, Required: true},
					"id":   {Type: TypeString, Computed: true},
					"port": {Type: TypeInt, Optional: true},
				},
			},
		},
	}

	elemType := cty.Object(map[string]cty.Type{
		"name": cty.String,
		"id":   cty.String,
		"port": cty.Number,
	})
	resType := cty.Object(map[string]cty.Type{
		"http_listener": cty.List(elemType),
	})

	// Prior state: items 1, 2, 4 in canonical order with matching IDs
	priorVal := cty.ObjectVal(map[string]cty.Value{
		"http_listener": cty.ListVal([]cty.Value{
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-1"),
				"id":   cty.StringVal("/sub/listeners/http-lstn-1"),
				"port": cty.NumberIntVal(80),
			}),
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-2"),
				"id":   cty.StringVal("/sub/listeners/http-lstn-2"),
				"port": cty.NumberIntVal(80),
			}),
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-4"),
				"id":   cty.StringVal("/sub/listeners/http-lstn-4"),
				"port": cty.NumberIntVal(80),
			}),
		}),
	})

	// Config: reordered in HCL as 4, 1, 2 (id is null)
	configVal := cty.ObjectVal(map[string]cty.Value{
		"http_listener": cty.ListVal([]cty.Value{
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-4"),
				"id":   cty.NullVal(cty.String),
				"port": cty.NumberIntVal(80),
			}),
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-1"),
				"id":   cty.NullVal(cty.String),
				"port": cty.NumberIntVal(80),
			}),
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-2"),
				"id":   cty.NullVal(cty.String),
				"port": cty.NumberIntVal(80),
			}),
		}),
	})

	// ProposedNewState from Core: Core naively merged config[i] with prior[i] by numerical index,
	// cross-wiring the IDs!
	proposedVal := cty.ObjectVal(map[string]cty.Value{
		"http_listener": cty.ListVal([]cty.Value{
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-4"),
				"id":   cty.StringVal("/sub/listeners/http-lstn-1"), // Corrupted by Core index-merge
				"port": cty.NumberIntVal(80),
			}),
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-1"),
				"id":   cty.StringVal("/sub/listeners/http-lstn-2"), // Corrupted by Core index-merge
				"port": cty.NumberIntVal(80),
			}),
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("http-lstn-2"),
				"id":   cty.StringVal("/sub/listeners/http-lstn-4"), // Corrupted by Core index-merge
				"port": cty.NumberIntVal(80),
			}),
		}),
	})

	_ = resType
	realigned := realignProposedNewStateForSortKeys(sch, proposedVal, priorVal, configVal)

	realignedList := realigned.GetAttr("http_listener").AsValueSlice()
	if len(realignedList) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(realignedList))
	}

	expected := []struct {
		name string
		id   string
	}{
		{"http-lstn-1", "/sub/listeners/http-lstn-1"},
		{"http-lstn-2", "/sub/listeners/http-lstn-2"},
		{"http-lstn-4", "/sub/listeners/http-lstn-4"},
	}

	for i, exp := range expected {
		elem := realignedList[i]
		actualName := elem.GetAttr("name").AsString()
		actualID := elem.GetAttr("id").AsString()

		if actualName != exp.name {
			t.Errorf("at index %d: expected name %q, got %q", i, exp.name, actualName)
		}
		if actualID != exp.id {
			t.Errorf("at index %d (%s): expected id %q, got %q", i, exp.name, exp.id, actualID)
		}
	}
}



