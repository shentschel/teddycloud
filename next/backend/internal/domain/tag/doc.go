// Package tag defines physical Tag identifier codecs.
//
// A UID is rendered as eight colon-separated hexadecimal bytes, for example
// 11:22:33:44:55:66:77:88. An rUID is the same bytes in reverse order and is
// rendered as sixteen compact hexadecimal digits, for example 8877665544332211.
// Parsers accept upper- or lower-case hexadecimal and always emit upper case.
// Physical identifiers must not be written to logs or error messages.
package tag
