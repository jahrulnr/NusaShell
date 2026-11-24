; C outline query — complete replacement of the stock v0.58.0 query.
; Upgrades struct_specifier to "struct" and adds enum/union specifiers.
(function_definition (function_declarator (identifier) @name)) @definition.function
(function_definition (function_declarator (field_identifier) @name)) @definition.function
(type_definition (type_identifier) @name) @definition.type
(struct_specifier (type_identifier) @name) @definition.struct
(union_specifier (type_identifier) @name) @definition.struct
(enum_specifier name: (type_identifier) @name) @definition.enum
(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call
