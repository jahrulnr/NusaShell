; Java outline query — complete replacement of the stock v0.58.0 query.
; Adds record_declaration and @interface (annotation_type_declaration).
(method_declaration (identifier) @name) @definition.method
(class_declaration (identifier) @name) @definition.class
(interface_declaration (identifier) @name) @definition.interface
(record_declaration (identifier) @name) @definition.record
(annotation_type_declaration (identifier) @name) @definition.interface
(enum_declaration (identifier) @name) @definition.type
(constructor_declaration (identifier) @name) @definition.constructor
(method_invocation (identifier) @name) @reference.call
