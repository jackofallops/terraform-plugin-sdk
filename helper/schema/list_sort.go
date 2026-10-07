package schema

import (
	"cmp"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

// canonicalizeList stably sorts a slice of elements according to the schema's
// SortKeys or SortFunc. If neither is specified, it returns the slice unchanged.
func (s *Schema) canonicalizeList(list []interface{}) []interface{} {
	sorted, _ := s.canonicalizeListWithPermutation(list)
	return sorted
}

// validateSortKeysUniqueness verifies that no two elements in list share identical compound
// values across sortKeys. Returns an error identifying the duplicate keys and offending indices.
func validateSortKeysUniqueness(list []interface{}, sortKeys []string) error {
	if len(sortKeys) == 0 || len(list) <= 1 {
		return nil
	}

	seen := make(map[string]int)
	for i, item := range list {
		m, ok := toMap(item)
		if !ok {
			continue
		}

		parts := make([]string, len(sortKeys))
		for kIdx, key := range sortKeys {
			val := m[key]
			if val == nil {
				val = ""
			}
			parts[kIdx] = fmt.Sprintf("%s=%v", key, val)
		}
		sig := strings.Join(parts, ", ")

		if firstIdx, exists := seen[sig]; exists {
			return fmt.Errorf("duplicate list element with sort keys [%s] found at indices %d and %d: compound sort keys must be unique", sig, firstIdx, i)
		}
		seen[sig] = i
	}

	return nil
}

// canonicalizeListWithPermutation stably sorts a slice of elements according to the schema's
// SortKeys or SortFunc. It returns the sorted slice and a permutation mapping where
// perm[sortedIndex] = originalIndex.
func (s *Schema) canonicalizeListWithPermutation(list []interface{}) ([]interface{}, []int) {
	n := len(list)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}

	if s == nil || s.Type != TypeList || n <= 1 || (s.SortFunc == nil && len(s.SortKeys) == 0) {
		result := make([]interface{}, n)
		copy(result, list)
		return result, perm
	}

	sort.SliceStable(perm, func(i, j int) bool {
		a := list[perm[i]]
		b := list[perm[j]]

		if s.SortFunc != nil {
			return s.SortFunc(a, b)
		}

		return compareByKeys(a, b, s.SortKeys)
	})

	result := make([]interface{}, n)
	for sortedIdx, origIdx := range perm {
		result[sortedIdx] = list[origIdx]
	}

	return result, perm
}

// compareByKeys compares two elements across the specified list of keys in sequence.
// Returns true if a < b.
func compareByKeys(a, b interface{}, keys []string) bool {
	mapA, okA := toMap(a)
	mapB, okB := toMap(b)

	// If either is not a map, place valid maps before nil/non-maps
	if !okA || !okB {
		if okA != okB {
			return okA
		}
		return false
	}

	for _, key := range keys {
		valA := mapA[key]
		valB := mapB[key]

		c := compareValues(valA, valB)
		if c != 0 {
			return c < 0
		}
	}

	return false
}

// toMap attempts to convert an element into a map[string]interface{}.
func toMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	switch m := v.(type) {
	case map[string]interface{}:
		return m, true
	case map[string]string:
		converted := make(map[string]interface{}, len(m))
		for k, val := range m {
			converted[k] = val
		}
		return converted, true
	}
	return nil, false
}

// compareValues compares two interface values. Returns:
// -1 if a < b
//
//	0 if a == b
//	1 if a > b
func compareValues(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}

	// Try numeric comparison first to avoid lexicographical ordering of numbers
	numA, isNumA := toFloat64(a)
	numB, isNumB := toFloat64(b)
	if isNumA && isNumB {
		return cmp.Compare(numA, numB)
	}

	// Try boolean comparison - unlikely, but cover all the bases.
	boolA, isBoolA := a.(bool)
	boolB, isBoolB := b.(bool)
	if isBoolA && isBoolB {
		if !boolA && boolB {
			return -1
		}
		if boolA && !boolB {
			return 1
		}
		return 0
	}

	// Fallback to string comparison
	strA := fmt.Sprintf("%v", a)
	strB := fmt.Sprintf("%v", b)
	if strA == "" && strB != "" {
		return 1
	}
	if strA != "" && strB == "" {
		return -1
	}
	return strings.Compare(strA, strB)
}

// toFloat64 attempts to convert any concrete integer or floating-point type into a float64.
// Returns the float64 representation and true if convertible, or (0, false) otherwise.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// realignPriorStateForSortKeys aligns prior state flatmap attributes (s.Attributes) for
// TypeList fields with SortKeys to match the canonical ordering of the incoming configuration.
// It migrates unchanged and server-computed attributes (e.g., remote IDs) with their logical
// block across additions, removals, and reorderings, and places deleted blocks at tail indices.
func realignPriorStateForSortKeys(schemaMap map[string]*Schema, s *terraform.InstanceState, c *terraform.ResourceConfig) {
	if s == nil || len(s.Attributes) == 0 || c == nil {
		return
	}

	for k, schema := range schemaMap {
		if schema.Type != TypeList || len(schema.SortKeys) == 0 {
			continue
		}

		if _, ok := schema.Elem.(*Resource); !ok {
			continue
		}

		countStr, ok := s.Attributes[k+".#"]
		if !ok || countStr == "0" {
			continue
		}
		oldCount, err := strconv.Atoi(countStr)
		if err != nil || oldCount <= 0 {
			continue
		}

		// Read old state blocks indexed by compound key
		type blockEntry struct {
			compoundKey string
			attrs       map[string]string
		}

		oldBlocks := make([]blockEntry, oldCount)
		oldBlockByKey := make(map[string]map[string]string)
		for oldIdx := 0; oldIdx < oldCount; oldIdx++ {
			oldPrefix := fmt.Sprintf("%s.%d.", k, oldIdx)
			blockAttrs := make(map[string]string)
			for attrK, attrV := range s.Attributes {
				if strings.HasPrefix(attrK, oldPrefix) {
					blockAttrs[attrK[len(oldPrefix):]] = attrV
				}
			}

			keyTuple := make([]string, len(schema.SortKeys))
			for i, sk := range schema.SortKeys {
				keyTuple[i] = fmt.Sprintf("%s=%v", sk, blockAttrs[sk])
			}
			compoundKey := strings.Join(keyTuple, ", ")

			oldBlocks[oldIdx] = blockEntry{compoundKey: compoundKey, attrs: blockAttrs}
			oldBlockByKey[compoundKey] = blockAttrs
		}

		// Read new config blocks in canonical order using ConfigFieldReader
		cfgReader := &ConfigFieldReader{Config: c, Schema: schemaMap}
		readRes, err := cfgReader.ReadField([]string{k})
		if err != nil || !readRes.Exists || readRes.Value == nil {
			continue
		}

		rawList, ok := readRes.Value.([]interface{})
		if !ok {
			continue
		}

		newCount := len(rawList)
		newAttrs := make(map[string]string)
		matchedOldKeys := make(map[string]bool)

		// 1. Assign surviving/reordered blocks to their new canonical indices
		for newIdx, item := range rawList {
			m, ok := toMap(item)
			if !ok {
				continue
			}

			keyTuple := make([]string, len(schema.SortKeys))
			for i, sk := range schema.SortKeys {
				val := m[sk]
				if val == nil {
					val = ""
				}
				keyTuple[i] = fmt.Sprintf("%s=%v", sk, val)
			}
			compoundKey := strings.Join(keyTuple, ", ")

			if priorAttrs, exists := oldBlockByKey[compoundKey]; exists {
				matchedOldKeys[compoundKey] = true
				newPrefix := fmt.Sprintf("%s.%d.", k, newIdx)
				for subK, subV := range priorAttrs {
					newAttrs[newPrefix+subK] = subV
				}
			}
		}

		// 2. Assign deleted blocks to tail indices (newCount, newCount+1, ...)
		tailIdx := newCount
		for _, oldEntry := range oldBlocks {
			if !matchedOldKeys[oldEntry.compoundKey] {
				newPrefix := fmt.Sprintf("%s.%d.", k, tailIdx)
				for subK, subV := range oldEntry.attrs {
					newAttrs[newPrefix+subK] = subV
				}
				tailIdx++
			}
		}

		// 3. Clear all old attributes under k.* from s.Attributes
		prefixWithDot := k + "."
		for attrK := range s.Attributes {
			if strings.HasPrefix(attrK, prefixWithDot) {
				delete(s.Attributes, attrK)
			}
		}

		// 4. Write back realigned attributes and total count
		totalCount := tailIdx
		if totalCount < oldCount {
			totalCount = oldCount
		}
		s.Attributes[k+".#"] = strconv.Itoa(totalCount)
		for newK, newV := range newAttrs {
			s.Attributes[newK] = newV
		}
	}
}

// realignProposedNewStateForSortKeys adjusts Terraform Core's index-based ProposedNewState merge
// for TypeList fields configured with SortKeys. Core merges prior[i] with config[i] positionally;
// this function correlates elements by their compound SortKeys instead, restores prior computed
// attributes onto the matching block, and sorts the proposed list into canonical order.
func realignProposedNewStateForSortKeys(schemaMap map[string]*Schema, proposed, prior, config cty.Value) cty.Value {
	if proposed.IsNull() || !proposed.IsKnown() || !proposed.Type().IsObjectType() {
		return proposed
	}

	hasSortKeys := false
	for _, sch := range schemaMap {
		if sch.Type == TypeList && len(sch.SortKeys) > 0 {
			hasSortKeys = true
			break
		}
	}
	if !hasSortKeys {
		return proposed
	}

	proposedMap := proposed.AsValueMap()
	modified := false

	for k, sch := range schemaMap {
		if sch.Type != TypeList || len(sch.SortKeys) == 0 {
			continue
		}
		childRes, ok := sch.Elem.(*Resource)
		if !ok {
			continue
		}
		childSchemaMap := childRes.SchemaMap()

		proposedList, exists := proposedMap[k]
		if !exists || proposedList.IsNull() || !proposedList.IsKnown() {
			continue
		}
		if !proposedList.Type().IsListType() && !proposedList.Type().IsTupleType() {
			continue
		}
		if proposedList.LengthInt() == 0 {
			continue
		}

		// Build map of prior elements by compound key
		priorElemByKey := make(map[string]cty.Value)
		if !prior.IsNull() && prior.IsKnown() && prior.Type().IsObjectType() && prior.Type().HasAttribute(k) {
			priorList := prior.GetAttr(k)
			if !priorList.IsNull() && priorList.IsKnown() && (priorList.Type().IsListType() || priorList.Type().IsTupleType()) {
				for _, priorElem := range priorList.AsValueSlice() {
					if priorElem.IsNull() || !priorElem.IsKnown() || !priorElem.Type().IsObjectType() {
						continue
					}
					sig, valid := extractSortKeySig(priorElem, sch.SortKeys)
					if valid {
						priorElemByKey[sig] = priorElem
					}
				}
			}
		}

		// Build map of config elements by compound key to determine which attributes were explicitly configured
		configElemByKey := make(map[string]cty.Value)
		if !config.IsNull() && config.IsKnown() && config.Type().IsObjectType() && config.Type().HasAttribute(k) {
			configList := config.GetAttr(k)
			if !configList.IsNull() && configList.IsKnown() && (configList.Type().IsListType() || configList.Type().IsTupleType()) {
				for _, cfgElem := range configList.AsValueSlice() {
					if cfgElem.IsNull() || !cfgElem.IsKnown() || !cfgElem.Type().IsObjectType() {
						continue
					}
					sig, valid := extractSortKeySig(cfgElem, sch.SortKeys)
					if valid {
						configElemByKey[sig] = cfgElem
					}
				}
			}
		}

		// Process proposed elements: restore correct computed attributes
		proposedSlice := proposedList.AsValueSlice()
		newSlice := make([]cty.Value, len(proposedSlice))

		for i, proposedElem := range proposedSlice {
			if proposedElem.IsNull() || !proposedElem.IsKnown() || !proposedElem.Type().IsObjectType() {
				newSlice[i] = proposedElem
				continue
			}

			sig, valid := extractSortKeySig(proposedElem, sch.SortKeys)
			if !valid {
				newSlice[i] = proposedElem
				continue
			}

			matchingPrior, hasPrior := priorElemByKey[sig]
			if !hasPrior {
				newSlice[i] = proposedElem
				continue
			}

			matchingConfig, hasConfig := configElemByKey[sig]
			elemAttrs := proposedElem.AsValueMap()
			elemModified := false

			for subName, subSch := range childSchemaMap {
				if !subSch.Computed {
					continue
				}

				// Check if configured in config
				configSpecified := false
				if hasConfig && matchingConfig.Type().HasAttribute(subName) {
					cfgVal := matchingConfig.GetAttr(subName)
					if !cfgVal.IsNull() && cfgVal.IsKnown() {
						configSpecified = true
					}
				}

				if !configSpecified {
					if matchingPrior.Type().HasAttribute(subName) {
						priorSubVal := matchingPrior.GetAttr(subName)
						if !priorSubVal.RawEquals(elemAttrs[subName]) {
							elemAttrs[subName] = priorSubVal
							elemModified = true
						}
					}
				}
			}

			if elemModified {
				newSlice[i] = cty.ObjectVal(elemAttrs)
			} else {
				newSlice[i] = proposedElem
			}
		}

		// Sort newSlice into canonical order by SortKeys
		sort.SliceStable(newSlice, func(i, j int) bool {
			return compareCtyElementsByKeys(newSlice[i], newSlice[j], sch.SortKeys)
		})

		var realignedList cty.Value
		if proposedList.Type().IsTupleType() {
			realignedList = cty.TupleVal(newSlice)
		} else {
			elemType := proposedList.Type().ElementType()
			if len(newSlice) == 0 {
				realignedList = cty.ListValEmpty(elemType)
			} else {
				realignedList = cty.ListVal(newSlice)
			}
		}

		proposedMap[k] = realignedList
		modified = true
	}

	if modified {
		return cty.ObjectVal(proposedMap)
	}
	return proposed
}

// extractSortKeySig constructs a deterministic string signature ("key1=val1, key2=val2")
// from a cty.Value object using the specified sortKeys. Returns ("", false) if the element
// is nil, unknown, or if any key attribute is missing or unknown.
func extractSortKeySig(elem cty.Value, sortKeys []string) (string, bool) {
	if elem.IsNull() || !elem.IsKnown() || !elem.Type().IsObjectType() {
		return "", false
	}
	parts := make([]string, len(sortKeys))
	for i, sk := range sortKeys {
		if !elem.Type().HasAttribute(sk) {
			return "", false
		}
		val := elem.GetAttr(sk)
		if val.IsNull() || !val.IsKnown() {
			return "", false
		}
		parts[i] = fmt.Sprintf("%s=%s", sk, ctyValueToString(val))
	}
	return strings.Join(parts, ", "), true
}

// ctyValueToString converts primitive cty values (string, number, bool) into a canonical
// string representation for signature hashing and fallback string comparison.
func ctyValueToString(v cty.Value) string {
	if v.IsNull() || !v.IsKnown() {
		return ""
	}
	switch v.Type() {
	case cty.String:
		return v.AsString()
	case cty.Number:
		bf := v.AsBigFloat()
		return bf.Text('f', -1)
	case cty.Bool:
		if v.True() {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%#v", v)
	}
}

// compareCtyElementsByKeys compares two cty object values across the provided sortKeys
// in sequence. Returns true if element a sorts before element b. Unknown or null attributes
// are sorted after known attributes.
func compareCtyElementsByKeys(a, b cty.Value, keys []string) bool {
	if a.IsNull() || !a.IsKnown() || !a.Type().IsObjectType() {
		return false
	}
	if b.IsNull() || !b.IsKnown() || !b.Type().IsObjectType() {
		return true
	}

	for _, key := range keys {
		if !a.Type().HasAttribute(key) || !b.Type().HasAttribute(key) {
			continue
		}
		valA := a.GetAttr(key)
		valB := b.GetAttr(key)

		cA := valA.IsNull() || !valA.IsKnown()
		cB := valB.IsNull() || !valB.IsKnown()
		if cA || cB {
			if cA != cB {
				return !cA
			}
			continue
		}

		c := compareCtyValues(valA, valB)
		if c != 0 {
			return c < 0
		}
	}
	return false
}

// compareCtyValues compares two primitive cty.Value instances. Numbers are compared
// as arbitrary-precision big floats, booleans sort false before true, and other types
// fall back to lexicographical string comparison. Returns -1 for a < b, 0 for a == b,
// and 1 for a > b.
func compareCtyValues(a, b cty.Value) int {
	tyA, tyB := a.Type(), b.Type()
	if tyA == cty.Number && tyB == cty.Number {
		bfA := a.AsBigFloat()
		bfB := b.AsBigFloat()
		return bfA.Cmp(bfB)
	}
	if tyA == cty.Bool && tyB == cty.Bool {
		if a.True() == b.True() {
			return 0
		}
		if !a.True() && b.True() {
			return -1
		}
		return 1
	}
	strA := ctyValueToString(a)
	strB := ctyValueToString(b)
	return cmp.Compare(strA, strB)
}
