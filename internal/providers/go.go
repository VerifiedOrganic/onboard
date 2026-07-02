package providers

// GoTagsQuery overrides gotreesitter's inferred Go tags query, whose unqualified
// (type_identifier) pattern captures return types as definitions.
const GoTagsQuery = `
(function_declaration name: (identifier) @name) @definition.function
(method_declaration name: (field_identifier) @name) @definition.method
(type_declaration (type_spec name: (type_identifier) @name)) @definition.type
(call_expression function: (identifier) @name) @reference.call
(call_expression function: (selector_expression field: (field_identifier) @name)) @reference.call
`
