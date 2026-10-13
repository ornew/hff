package sql

import "github.com/ornew/hff"

// PostgreSQL folds base64 output at 76 characters; HFF emits one line.
func postgresBase64(code string) string {
	return "replace(encode(" + code + ",'base64'),chr(10),'')"
}

func (b *builder) bytesCall(n hff.Node, name string, receiver expression) (expression, error) {
	if n.Type().Kind() == hff.NullType || n.Type().Kind() == hff.NeverType || receiver.kind == hff.NullType {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	if name == "frombase64" || name == "frombase64r" {
		text, err := b.stringConversionInput(n, receiver)
		if err != nil {
			return expression{}, err
		}
		input := b.project(text)
		// PostgreSQL's decoder accepts noncanonical spellings and can throw on
		// malformed input. Sanitize first, then verify the exact round trip.
		valid := "(" + input + " COLLATE \"C\" ~ '^[A-Za-z0-9+/]*={0,2}$' AND length(" + input + ")%4=0)"
		payload := input
		if name == "frombase64r" {
			valid = "(" + input + " COLLATE \"C\" ~ '^[A-Za-z0-9+/]*$' AND length(" + input + ")%4<>1)"
			payload = "(" + input + " || repeat('=',(4-length(" + input + ")%4)%4))"
		}
		safe := b.project("(CASE WHEN " + valid + " THEN " + payload + " ELSE NULL::text END)")
		decoded := b.project("decode(" + safe + ",'base64')")
		encoded := postgresBase64(decoded)
		if name == "frombase64r" {
			encoded = "rtrim(" + encoded + ",'=')"
		}
		code := "(CASE WHEN " + encoded + " COLLATE \"C\"=" + input + " COLLATE \"C\" THEN " + decoded + " ELSE NULL::bytea END)"
		return expression{code, receiver.present, hff.BytesType}, nil
	}
	if receiver.kind != hff.BytesType {
		return expression{}, b.failure(n, "PostgreSQL bytes methods require a native bytea representation")
	}
	if name == "len" {
		return expression{"octet_length(" + receiver.code + ")::bigint", receiver.present, hff.IntType}, nil
	}
	encoded := postgresBase64(receiver.code)
	if name == "base64r" {
		encoded = "rtrim(" + encoded + ",'=')"
	}
	return expression{encoded, receiver.present, hff.StringType}, nil
}

// postgresDecodeBase64 rejects malformed input before invoking PostgreSQL's
// throwing decoder and checks canonical padding bits by encoding the result.
func postgresDecodeBase64(text string) string {
	valid := "(" + text + " COLLATE \"C\" ~ '^[A-Za-z0-9+/]*={0,2}$' AND length(" + text + ")%4=0)"
	safe := "(CASE WHEN " + valid + " THEN " + text + " ELSE NULL::text END)"
	decoded := "decode(" + safe + ",'base64')"
	return "(CASE WHEN " + postgresBase64(decoded) + " COLLATE \"C\"=" + text + " COLLATE \"C\" THEN " + decoded + " ELSE NULL::bytea END)"
}
