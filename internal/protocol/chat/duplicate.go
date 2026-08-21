package chat

// hasDuplicateTopLevelKeys scans the JSON payload for duplicate object keys at
// the root level. Returns true if any key appears more than once. The scan is
// shallow — nested objects are skipped as opaque values.
//
// Used to detect when the fast path cannot safely forward raw bytes: json.Unmarshal
// silently deduplicates (last-write-wins), so the router's validated fields map
// differs from what the raw bytes contain. Forwarding those bytes to an upstream
// with strict schema validation (e.g., NIM) may cause 422 on duplicate keys the
// router never saw.
func hasDuplicateTopLevelKeys(payload []byte) bool {
	scanner := newJSONScanner(payload)
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.data) || scanner.data[scanner.pos] != '{' {
		return false // not an object
	}
	scanner.pos++
	seen := make(map[string]struct{})
	for scanner.more('}') {
		key, err := nextJSONObjectKey(scanner)
		if err != nil {
			return false // malformed, let Parse handle it
		}
		if _, exists := seen[key]; exists {
			return true // duplicate found
		}
		seen[key] = struct{}{}
		if err := skipJSONValue(scanner); err != nil {
			return false
		}
	}
	return false
}
