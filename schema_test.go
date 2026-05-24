package finemcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

// --- Primitive types ---

func TestGenerateSchema_String(t *testing.T) {
	t.Parallel()
	type In struct {
		Name string `json:"name"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	assertSchemaType(t, schema, "object")
	assertPropertyType(t, schema, "name", "string")
	assertRequired(t, schema, "name")
}

func TestGenerateSchema_Int(t *testing.T) {
	t.Parallel()
	type In struct {
		Count int `json:"count"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	assertPropertyType(t, schema, "count", "integer")
	assertRequired(t, schema, "count")
}

func TestGenerateSchema_IntVariants(t *testing.T) {
	t.Parallel()
	type In struct {
		A int8  `json:"a"`
		B int16 `json:"b"`
		C int32 `json:"c"`
		D int64 `json:"d"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	for _, f := range []string{"a", "b", "c", "d"} {
		assertPropertyType(t, schema, f, "integer")
	}
}

func TestGenerateSchema_UintVariants(t *testing.T) {
	t.Parallel()
	type In struct {
		A uint   `json:"a"`
		B uint8  `json:"b"`
		C uint16 `json:"c"`
		D uint32 `json:"d"`
		E uint64 `json:"e"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	for _, f := range []string{"a", "b", "c", "d", "e"} {
		assertPropertyType(t, schema, f, "integer")
	}
}

func TestGenerateSchema_Float(t *testing.T) {
	t.Parallel()
	type In struct {
		Value float64 `json:"value"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	assertPropertyType(t, schema, "value", "number")
}

func TestGenerateSchema_Float32(t *testing.T) {
	t.Parallel()
	type In struct {
		Value float32 `json:"value"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	assertPropertyType(t, schema, "value", "number")
}

func TestGenerateSchema_Bool(t *testing.T) {
	t.Parallel()
	type In struct {
		Active bool `json:"active"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	assertPropertyType(t, schema, "active", "boolean")
	assertRequired(t, schema, "active")
}

// --- Omitempty → optional ---

func TestGenerateSchema_Omitempty_NotRequired(t *testing.T) {
	t.Parallel()
	type In struct {
		Name  string `json:"name"`
		Label string `json:"label,omitempty"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	assertRequired(t, schema, "name")
	assertNotRequired(t, schema, "label")
}

// --- Pointer → optional ---

func TestGenerateSchema_Pointer_NotRequired(t *testing.T) {
	t.Parallel()
	type In struct {
		Name  string  `json:"name"`
		Alias *string `json:"alias"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	assertRequired(t, schema, "name")
	assertNotRequired(t, schema, "alias")
	assertPropertyType(t, schema, "alias", "string")
}

// --- json:"-" → skip ---

func TestGenerateSchema_JsonDash_Skipped(t *testing.T) {
	t.Parallel()
	type In struct {
		Public  string `json:"public"`
		Private string `json:"-"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	if _, ok := props["Private"]; ok {
		t.Error("field with json:\"-\" should be skipped")
	}
	if _, ok := props["private"]; ok {
		t.Error("field with json:\"-\" should be skipped")
	}
}

// --- description tag ---

func TestGenerateSchema_DescriptionTag(t *testing.T) {
	t.Parallel()
	type In struct {
		Name string `json:"name" description:"The user's name"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	prop := props["name"].(map[string]any)
	desc, ok := prop["description"]
	if !ok {
		t.Fatal("missing description")
	}
	if desc != "The user's name" {
		t.Errorf("description = %q, want %q", desc, "The user's name")
	}
}

// --- Nested struct ---

func TestGenerateSchema_NestedStruct(t *testing.T) {
	t.Parallel()
	type Address struct {
		City string `json:"city"`
		Zip  string `json:"zip"`
	}
	type In struct {
		Name    string  `json:"name"`
		Address Address `json:"address"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)

	addrSchema := props["address"].(map[string]any)
	if addrSchema["type"] != "object" {
		t.Errorf("nested struct type = %v, want object", addrSchema["type"])
	}

	addrProps := addrSchema["properties"].(map[string]any)
	if _, ok := addrProps["city"]; !ok {
		t.Error("missing nested property: city")
	}
	if _, ok := addrProps["zip"]; !ok {
		t.Error("missing nested property: zip")
	}
}

// --- Slice ---

func TestGenerateSchema_Slice(t *testing.T) {
	t.Parallel()
	type In struct {
		Tags []string `json:"tags"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	tagSchema := props["tags"].(map[string]any)

	if tagSchema["type"] != "array" {
		t.Errorf("slice type = %v, want array", tagSchema["type"])
	}

	items := tagSchema["items"].(map[string]any)
	if items["type"] != "string" {
		t.Errorf("items type = %v, want string", items["type"])
	}
}

func TestGenerateSchema_SliceOfStructs(t *testing.T) {
	t.Parallel()
	type Item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	type In struct {
		Items []Item `json:"items"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	itemsSchema := props["items"].(map[string]any)

	if itemsSchema["type"] != "array" {
		t.Fatalf("type = %v, want array", itemsSchema["type"])
	}
	items := itemsSchema["items"].(map[string]any)
	if items["type"] != "object" {
		t.Errorf("items type = %v, want object", items["type"])
	}
}

// --- Map ---

func TestGenerateSchema_MapStringAny(t *testing.T) {
	t.Parallel()
	type In struct {
		Meta map[string]any `json:"meta"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	metaSchema := props["meta"].(map[string]any)

	if metaSchema["type"] != "object" {
		t.Errorf("map type = %v, want object", metaSchema["type"])
	}
}

func TestGenerateSchema_MapStringString(t *testing.T) {
	t.Parallel()
	type In struct {
		Labels map[string]string `json:"labels"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	labelsSchema := props["labels"].(map[string]any)

	if labelsSchema["type"] != "object" {
		t.Errorf("type = %v, want object", labelsSchema["type"])
	}
	addl := labelsSchema["additionalProperties"].(map[string]any)
	if addl["type"] != "string" {
		t.Errorf("additionalProperties type = %v, want string", addl["type"])
	}
}

// --- json.RawMessage → any ---

func TestGenerateSchema_RawMessage(t *testing.T) {
	t.Parallel()
	type In struct {
		Data json.RawMessage `json:"data"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	dataSchema := props["data"].(map[string]any)

	// json.RawMessage should produce an empty schema (any)
	if len(dataSchema) != 0 {
		t.Errorf("json.RawMessage schema should be empty (any), got %v", dataSchema)
	}
}

// --- interface{} / any → empty schema ---

func TestGenerateSchema_AnyField(t *testing.T) {
	t.Parallel()
	type In struct {
		Payload any `json:"payload"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	payloadSchema := props["payload"].(map[string]any)

	if len(payloadSchema) != 0 {
		t.Errorf("any schema should be empty, got %v", payloadSchema)
	}
}

// --- Empty struct ---

func TestGenerateSchema_EmptyStruct(t *testing.T) {
	t.Parallel()
	type In struct{}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	assertSchemaType(t, schema, "object")
	props := schema["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("expected no properties, got %d", len(props))
	}
}

// --- Non-struct type (e.g., string) ---

func TestGenerateSchema_NonStruct(t *testing.T) {
	t.Parallel()
	schema := GenerateSchema(reflect.TypeOf("hello"))
	if schema["type"] != "string" {
		t.Errorf("string type schema = %v, want string", schema["type"])
	}
}

// --- Multiple required + optional fields ---

func TestGenerateSchema_MixedRequired(t *testing.T) {
	t.Parallel()
	type In struct {
		Name     string  `json:"name"`
		Email    string  `json:"email"`
		Nickname string  `json:"nickname,omitempty"`
		Age      *int    `json:"age"`
		Bio      *string `json:"bio,omitempty"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))

	assertRequired(t, schema, "name")
	assertRequired(t, schema, "email")
	assertNotRequired(t, schema, "nickname")
	assertNotRequired(t, schema, "age")
	assertNotRequired(t, schema, "bio")
}

// --- No json tag → use field name ---

func TestGenerateSchema_NoJsonTag_UsesFieldName(t *testing.T) {
	t.Parallel()
	type In struct {
		UserName string
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))
	props := schema["properties"].(map[string]any)
	if _, ok := props["UserName"]; !ok {
		t.Error("expected property 'UserName' for field without json tag")
	}
}

// --- Valid JSON output ---

func TestGenerateSchema_ValidJSON(t *testing.T) {
	t.Parallel()
	type Addr struct {
		City string `json:"city"`
	}
	type In struct {
		Name    string   `json:"name" description:"Full name"`
		Age     int      `json:"age,omitempty"`
		Tags    []string `json:"tags"`
		Address Addr     `json:"address"`
	}
	schema := GenerateSchema(reflect.TypeOf(In{}))

	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	var roundtrip map[string]any
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("schema JSON does not roundtrip: %v", err)
	}
}

// --- Helper assertions ---

func assertSchemaType(t *testing.T, schema map[string]any, expected string) {
	t.Helper()
	if schema["type"] != expected {
		t.Errorf("schema type = %v, want %s", schema["type"], expected)
	}
}

func assertPropertyType(t *testing.T, schema map[string]any, prop, expected string) {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties")
	}
	p, ok := props[prop].(map[string]any)
	if !ok {
		t.Fatalf("missing property %q", prop)
	}
	if p["type"] != expected {
		t.Errorf("property %q type = %v, want %s", prop, p["type"], expected)
	}
}

func assertRequired(t *testing.T, schema map[string]any, field string) {
	t.Helper()
	req, _ := schema["required"].([]string)
	for _, r := range req {
		if r == field {
			return
		}
	}
	t.Errorf("field %q should be required, required=%v", field, req)
}

func assertNotRequired(t *testing.T, schema map[string]any, field string) {
	t.Helper()
	req, _ := schema["required"].([]string)
	for _, r := range req {
		if r == field {
			t.Errorf("field %q should NOT be required, required=%v", field, req)
			return
		}
	}
}

// --- Recursion / depth protection ---

// SelfRef is a self-referential type for testing cycle detection.
type SelfRef struct {
	Name string   `json:"name"`
	Next *SelfRef `json:"next"`
}

func TestGenerateSchema_SelfReferential_NoPanic(t *testing.T) {
	t.Parallel()

	// Must not panic or stack overflow.
	schema := GenerateSchema(reflect.TypeOf(SelfRef{}))
	assertSchemaType(t, schema, "object")
	assertPropertyType(t, schema, "name", "string")

	// The "next" property should exist and be an object (cycle truncated).
	props := schema["properties"].(map[string]any)
	nextSchema, ok := props["next"].(map[string]any)
	if !ok {
		t.Fatal("expected 'next' property")
	}
	if nextSchema["type"] != "object" {
		t.Errorf("next type = %v, want object", nextSchema["type"])
	}
}

// MutualA and MutualB form a mutual recursion cycle.
type MutualA struct {
	Name string   `json:"name"`
	B    *MutualB `json:"b"`
}

type MutualB struct {
	Value int      `json:"value"`
	A     *MutualA `json:"a"`
}

func TestGenerateSchema_MutualRecursion_NoPanic(t *testing.T) {
	t.Parallel()

	schema := GenerateSchema(reflect.TypeOf(MutualA{}))
	assertSchemaType(t, schema, "object")
	assertPropertyType(t, schema, "name", "string")

	// Should have a "b" property that is an object.
	props := schema["properties"].(map[string]any)
	bSchema := props["b"].(map[string]any)
	if bSchema["type"] != "object" {
		t.Errorf("b type = %v, want object", bSchema["type"])
	}
}

func TestGenerateSchema_DeepNesting_Truncates(t *testing.T) {
	t.Parallel()

	// SelfRef produces a chain of depth maxSchemaDepth (20).
	// Walk the schema chain and verify it terminates.
	schema := GenerateSchema(reflect.TypeOf(SelfRef{}))

	depth := 0
	current := schema
	for depth < maxSchemaDepth+5 { // safety limit
		props, ok := current["properties"].(map[string]any)
		if !ok {
			break // truncated — no more properties
		}
		nextProp, ok := props["next"].(map[string]any)
		if !ok {
			break
		}
		// If the next level has no properties, it was truncated.
		if _, hasSub := nextProp["properties"]; !hasSub {
			depth++
			break
		}
		current = nextProp
		depth++
	}

	if depth > maxSchemaDepth {
		t.Errorf("schema depth = %d, should be <= %d", depth, maxSchemaDepth)
	}
}

func TestGenerateSchema_SliceOfSelfRef_NoPanic(t *testing.T) {
	t.Parallel()

	type Input struct {
		Nodes []SelfRef `json:"nodes"`
	}

	schema := GenerateSchema(reflect.TypeOf(Input{}))
	assertSchemaType(t, schema, "object")

	props := schema["properties"].(map[string]any)
	nodesSchema := props["nodes"].(map[string]any)
	if nodesSchema["type"] != "array" {
		t.Errorf("nodes type = %v, want array", nodesSchema["type"])
	}
}
