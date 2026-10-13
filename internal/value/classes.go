package value

// ClassObj is a class at run time.
type ClassObj struct {
	Name       string
	Base       *ClassObj
	FieldNames []string              // instance data members, inherited ones first
	FieldIndex map[string]int        // name -> slot in ObjectObj.Fields
	Methods    map[string]*MethodObj // own and inherited member functions (overrides resolved)
	Statics    []Value               // the class's own static data members
	Ctor       *MethodObj            // C::C, else the nearest base class's constructor, or nil
}

// MethodObj is a member function of a class.
type MethodObj struct {
	Name   string
	Class  *ClassObj // the class that declares it
	Static bool
	Fn     Named // the compiled function; nil if declared but not defined
}

// FullName is "Class::name".
func (m *MethodObj) FullName() string { return m.Class.Name + "::" + m.Name }

// ObjectObj is an instance of a class.
type ObjectObj struct {
	Class  *ClassObj
	Fields []Value
	Native any // data the VM attaches (the stack trace of a thrown Error)
}

// BoundMethodObj is a method together with the object it was taken from
// (the value of p->m without a call).
type BoundMethodObj struct {
	Recv   Value
	Method *MethodObj
}

func MakeClass(c *ClassObj) Value { return Value{kind: Class, obj: c} }

// NewObject makes an object of class c with all data members nil.
func NewObject(c *ClassObj) Value {
	return Value{kind: Object, obj: &ObjectObj{Class: c, Fields: make([]Value, len(c.FieldNames))}}
}

func MakeBoundMethod(recv Value, m *MethodObj) Value {
	return Value{kind: BoundMethod, obj: &BoundMethodObj{Recv: recv, Method: m}}
}

func (v Value) AsClass() *ClassObj             { return v.obj.(*ClassObj) }
func (v Value) AsObject() *ObjectObj           { return v.obj.(*ObjectObj) }
func (v Value) AsBoundMethod() *BoundMethodObj { return v.obj.(*BoundMethodObj) }

// IsSubclassOf reports whether c is k or derives from k.
func (c *ClassObj) IsSubclassOf(k *ClassObj) bool {
	for ; c != nil; c = c.Base {
		if c == k {
			return true
		}
	}
	return false
}

// Member returns the value of p->name: a data member, or else a method
// (bound to p; a static method gives its plain function).
func Member(p Value, name string, function func(Named) Value) (Value, error) {
	if p.kind != Object {
		return NilValue, Errorf(TypeError, "cannot get member %s of %s", name, p.kind)
	}
	o := p.AsObject()
	if i, ok := o.Class.FieldIndex[name]; ok {
		return o.Fields[i], nil
	}
	if m, ok := o.Class.Methods[name]; ok {
		if m.Fn == nil {
			return NilValue, Errorf(MemberError, "%s is declared but not defined", m.FullName())
		}
		if m.Static {
			return function(m.Fn), nil
		}
		return MakeBoundMethod(p, m), nil
	}
	return NilValue, Errorf(MemberError, "class %s has no member %s", o.Class.Name, name)
}

// SetMember does p->name = v; name must be a data member.
func SetMember(p Value, name string, v Value) error {
	if p.kind != Object {
		return Errorf(TypeError, "cannot set member %s of %s", name, p.kind)
	}
	o := p.AsObject()
	if i, ok := o.Class.FieldIndex[name]; ok {
		o.Fields[i] = v
		return nil
	}
	if _, ok := o.Class.Methods[name]; ok {
		return Errorf(MemberError, "cannot assign to member function %s of class %s", name, o.Class.Name)
	}
	return Errorf(MemberError, "class %s has no data member %s", o.Class.Name, name)
}

// Method finds the member function p->name(...) calls.
func Method(p Value, name string) (*MethodObj, error) {
	if p.kind != Object {
		return nil, Errorf(TypeError, "cannot call method %s on %s", name, p.kind)
	}
	c := p.AsObject().Class
	if m, ok := c.Methods[name]; ok {
		return m, nil
	}
	if _, ok := c.FieldIndex[name]; ok {
		return nil, Errorf(MemberError, "%s is a data member of class %s, not a member function (call its value with (p->%s)(...))", name, c.Name, name)
	}
	return nil, Errorf(MemberError, "class %s has no member function %s", c.Name, name)
}
