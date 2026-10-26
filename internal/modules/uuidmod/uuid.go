// Package uuidmod is the native module "uuid": new UUIDs as strings
// (TODO.md Q58).
//
//	use uuid;
//	println(uuid::v7());                 // 019a1b2c-3d4e-7f60-8a9b-0c1d2e3f4a5b
//	println(uuid::valid("not a uuid"));  // false
//
// Version 7 is ordered by time (its first 48 bits are the time in
// milliseconds), so new keys land at the end of a database index; version
// 4 is entirely random. The db module's object mapping uses version 7 for
// a key declared "UUID". Versions 5 and 3 are derived from a namespace and
// a name (RFC 9562 section 5.5 and 5.3), so the same name always gives the
// same UUID; the module has the standard namespaces as constants.
package uuidmod

import (
	"github.com/google/uuid"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "uuid",
		Doc:  "UUIDs (versions 7, 4, 5 and 3) as strings",
		Funcs: map[string]native.Func{
			"v7": func(env value.Env, args []value.Value) (value.Value, error) {
				if _, err := native.Check("uuid::v7", args, 0, 0); err != nil {
					return value.NilValue, err
				}
				return value.MakeString(V7()), nil
			},
			"v4": func(env value.Env, args []value.Value) (value.Value, error) {
				if _, err := native.Check("uuid::v4", args, 0, 0); err != nil {
					return value.NilValue, err
				}
				return value.MakeString(uuid.NewString()), nil
			},
			"v5": named("uuid::v5", uuid.NewSHA1),
			"v3": named("uuid::v3", uuid.NewMD5),
			"valid": func(env value.Env, args []value.Value) (value.Value, error) {
				a, err := native.Check("uuid::valid", args, 1, 1)
				if err != nil {
					return value.NilValue, err
				}
				s, err := a.String(0)
				if err != nil {
					return value.NilValue, err
				}
				return value.MakeBool(Valid(s)), nil
			},
		},
		Consts: map[string]value.Value{
			"DNS":  value.MakeString(uuid.NameSpaceDNS.String()),
			"URL":  value.MakeString(uuid.NameSpaceURL.String()),
			"OID":  value.MakeString(uuid.NameSpaceOID.String()),
			"X500": value.MakeString(uuid.NameSpaceX500.String()),
		},
	})
}

// named is uuid::v5 or uuid::v3: the UUID of name (a string) in namespace
// ns (a UUID in its usual form), made by hash.
func named(fname string, hash func(uuid.UUID, []byte) uuid.UUID) native.Func {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		a, err := native.Check(fname, args, 2, 2)
		if err != nil {
			return value.NilValue, err
		}
		ns, err := a.String(0)
		if err != nil {
			return value.NilValue, err
		}
		name, err := a.String(1)
		if err != nil {
			return value.NilValue, err
		}
		if !Valid(ns) {
			return value.NilValue, a.Errorf(value.ValueError, "the namespace %q is not a UUID", ns)
		}
		return value.MakeString(hash(uuid.MustParse(ns), []byte(name)).String()), nil
	}
}

// V7 gives a new version 7 UUID in its usual form, lower case.
func V7() string {
	return uuid.Must(uuid.NewV7()).String()
}

// Valid reports whether s is a UUID in its usual form: 32 hexadecimal
// digits in groups of 8, 4, 4, 4 and 12, joined by "-" (either case).
func Valid(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}
