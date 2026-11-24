; TSX outline query — same coverage as typescript.scm.
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
