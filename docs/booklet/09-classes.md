# 9. Classes and member functions

[The booklet](README.md) · previous: [8. Closures](08-closures.md) · next: [10. Exceptions](10-exceptions.md)

A class describes a kind of object: the data each one holds (its *data
members*) and what it can do (its *member functions*). Kukel's classes
look like C++'s: the class lists its members, and the member functions
are written after it.

## A class

```
class Item {
    name, price;                 // data members
    Item(name, price);           // the constructor
    total(count);                // a member function
    label();
}

Item::Item(name, price) {
    this->name = name;
    this->price = price;
}

Item::total(count) { return price * count; }
Item::label() { return name + " (" + str(price) + ")"; }

main() {
    local pen = new Item("pen", 1.5);
    println(pen->label(), pen->total(4));
    pen->price = 2;
    println(pen->name, pen->price, pen);
}
```

```
$ kukel run item.kkl
pen (1.5) 6.0
pen 2 Item{name: "pen", price: 2}
```

- `class Name { ... }` declares the members: data members first (`name,
  price;`), then the member functions with their parameters.
- Each member function is defined after the class, as
  `Class::function(parameters) { body }`.
- The *constructor* is the member function named like the class. `new
  Item("pen", 1.5)` makes an object, with every data member `nil`, and
  then runs the constructor on it.
- Inside a member function, the members are reached by their name alone
  (`price`, `label()`); `this` is the object itself. Where a parameter has
  the same name as a member, `this->name` is the member.
- Outside, `->` reaches a member: `pen->price`, `pen->label()`. All
  members are public.
- An object prints with its class and its data members.

Objects are shared, as vectors are (chapter 2): passing `pen` to a
function passes the same object, not a copy.

## Deriving a class

A class can *derive* from another: it gets all the members of its *base
class*, and adds its own or replaces some:

```
class Item {
    name, price;
    Item(name, price);
    total(count);
    label();
}

Item::Item(name, price) {
    this->name = name;
    this->price = price;
}

Item::total(count) { return price * count; }
Item::label() { return name + " (" + str(price) + ")"; }

class Sale : Item {
    percent;
    Sale(name, price, percent);
    total(count);
    label();
}

Sale::Sale(name, price, percent) {
    Item::Item(name, price);          // the base class's constructor
    this->percent = percent;
}

Sale::total(count) { return Item::total(count) * (100 - percent) / 100.0; }
Sale::label() { return Item::label() + ", " + str(percent) + "% off"; }

main() {
    local cart = [new Item("pen", 2), new Sale("ink", 10, 25)];

    for (local item in cart) println(item->label(), "x3:", item->total(3));
    println(instanceof(cart[1], Item), classof(cart[1]) == Sale, classof(cart[0]));
}
```

```
$ kukel run sale.kkl
pen (2) x3: 6
ink (10), 25% off x3: 22.5
true true <class Item>
```

- `class Sale : Item` derives `Sale` from `Item`; a class has at most one
  base class.
- The base class's constructor is not called by itself: call it, as
  `Item::Item(name, price)`.
- `Sale` declares `total` and `label` again, so they *override* `Item`'s.
  `item->total(3)` runs the version of the object's own class, whatever
  the variable is (every member function is *virtual*).
- `Item::total(count)` runs `Item`'s version on the same object: the way
  to build on what the base class does.
- `instanceof(x, C)` says whether `x` is an object of `C` or of a class
  derived from it; `classof(x)` gives its class.
- A class without a constructor of its own uses its base class's.

## Static members

A `static` member belongs to the class, not to each object: one value
shared by all, or a function that needs no object:

```
class Ticket {
    number;
    static issued;
    Ticket();
    static count();
}

Ticket::Ticket() {
    issued = issued == nil ? 1 : issued + 1;
    number = issued;
}

Ticket::count() { return issued; }

main() {
    local a = new Ticket(), b = new Ticket();
    println(a->number, b->number, Ticket::count(), Ticket::issued);
}
```

```
$ kukel run ticket.kkl
1 2 2 2
```

Outside the class, a static member is written with the class:
`Ticket::count()`, `Ticket::issued`. A static member function has no
`this`.

## Declared, not defined

A member function may be declared in a class and left undefined, for the
derived classes to define. A base class can then call it, and get the
derived class's version:

```
class Shape {
    area();                     // each kind of shape defines it
    describe();
}

Shape::describe() { return "a shape of area " + str(area()); }

class Square : Shape {
    side;
    Square(side);
    area();
}

Square::Square(side) { this->side = side; }
Square::area() { return side * side; }

main() { println(new Square(3)->describe()); }
```

```
$ kukel run shape.kkl
a shape of area 9
```

Calling `area()` on a plain `Shape` would be an error (a `MemberError`:
declared but not defined).

## Classes that work with the language

Some member functions have a meaning to the language itself:

- `close()`: `with` calls it when its statement ends (chapter 6).
- `iter()`: `for` walks an object whose `iter()` gives a generator
  (chapter 11).

The built-in class `Error` is the base class of every exception (next
chapter).

[The booklet](README.md) · previous: [8. Closures](08-closures.md) · next: [10. Exceptions](10-exceptions.md)
