; JavaScript outline query — complete replacement of the stock v0.58.0 query.
; Adds generator functions and `const f = () => {}` / `var f = function() {}`
; function bindings, which dominate modern JS source.
(function_declaration (identifier) @name) @definition.function
(generator_function_declaration (identifier) @name) @definition.function
(method_definition (property_identifier) @name) @definition.method
(class_declaration (identifier) @name) @definition.class
(lexical_declaration (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])) @definition.function
(variable_declaration (variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)])) @definition.function
(call_expression (identifier) @name) @reference.call
(call_expression (member_expression (property_identifier) @name)) @reference.call
