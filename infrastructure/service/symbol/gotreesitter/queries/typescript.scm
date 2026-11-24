; TypeScript outline query — complete replacement of the stock v0.58.0 query.
; Adds: type_alias_declaration, abstract_class_declaration, internal_module
; (namespace/module blocks), generator functions, and `const f = () => {}`
; function bindings. enum keeps the stock "type" kind.
(function_declaration (identifier) @name) @definition.function
(generator_function_declaration (identifier) @name) @definition.function
(method_definition (property_identifier) @name) @definition.method
(class_declaration (type_identifier) @name) @definition.class
(abstract_class_declaration (type_identifier) @name) @definition.class
(interface_declaration (type_identifier) @name) @definition.interface
(type_alias_declaration name: (type_identifier) @name) @definition.type
(internal_module name: (_) @name) @definition.module
(enum_declaration (identifier) @name) @definition.enum
(lexical_declaration (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])) @definition.function
(variable_declaration (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])) @definition.function
(call_expression (identifier) @name) @reference.call
(call_expression (member_expression (property_identifier) @name)) @reference.call
