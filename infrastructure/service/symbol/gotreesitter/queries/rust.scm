; Rust outline query — complete replacement of the stock v0.58.0 query.
; Upgrades struct/enum/trait from the stock catch-all "type" kind, and adds
; impl blocks (methods outline under their target type), mod items,
; macro_rules! definitions, unions, type aliases, and consts/statics.
(function_item (identifier) @name) @definition.function
(function_signature_item (identifier) @name) @definition.function
(struct_item (type_identifier) @name) @definition.struct
(enum_item (type_identifier) @name) @definition.enum
(trait_item (type_identifier) @name) @definition.trait
(union_item (type_identifier) @name) @definition.struct
(type_item (type_identifier) @name) @definition.type
(impl_item type: (_) @name) @definition.impl
(mod_item (identifier) @name) @definition.module
(macro_definition (identifier) @name) @definition.macro
(const_item (identifier) @name) @definition.constant
(static_item (identifier) @name) @definition.constant
(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call
(call_expression (scoped_identifier (identifier) @name)) @reference.call
(macro_invocation (identifier) @name) @reference.call
