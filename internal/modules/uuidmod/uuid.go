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
// a key declared "UUID".
package uuidmod

import (
	"github.com/google/uuid"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "uuid",
		Doc:  "UUIDs (versions 7 and 4) as strings",
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
	})
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
