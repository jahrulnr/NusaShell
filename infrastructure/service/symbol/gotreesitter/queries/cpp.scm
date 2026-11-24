; C++ outline query — complete replacement of the stock v0.58.0 query.
; Adds named namespaces and enum_specifier.
(function_definition (function_declarator (identifier) @name)) @definition.function
(function_definition (function_declarator (field_identifier) @name)) @definition.function
(type_definition (type_identifier) @name) @definition.type
(class_specifier (type_identifier) @name) @definition.class
(struct_specifier (type_identifier) @name) @definition.struct
(enum_specifier name: (type_identifier) @name) @definition.enum
(namespace_definition name: (namespace_identifier) @name) @definition.module
(alias_declaration name: (type_identifier) @name) @definition.type
(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call
