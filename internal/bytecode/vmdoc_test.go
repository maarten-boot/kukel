package bytecode

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestVMDocMatchesInstructionSet checks the instruction table of
// docs/vm.md against the code: every opcode is documented once, with its
// number, name, operands (names and widths) and stack effect.
func TestVMDocMatchesInstructionSet(t *testing.T) {
	doc, err := os.ReadFile("../../docs/vm.md")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[Op]bool{}
	for _, line := range strings.Split(string(doc), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 8 {
			continue
		}
		num, err := strconv.Atoi(strings.TrimSpace(cells[1]))
		if err != nil {
			continue // not an instruction row
		}
		op := Op(num)
		info, ok := Info(op)
		if !ok {
			t.Errorf("docs/vm.md documents opcode %d, which does not exist", num)
			continue
		}
		if seen[op] {
			t.Errorf("opcode %d (%s) is documented twice", num, info.Name)
		}
		seen[op] = true
		if name := strings.Trim(strings.TrimSpace(cells[2]), "`"); name != info.Name {
			t.Errorf("opcode %d: documented as %s, is %s", num, name, info.Name)
		}

		// Operands: "name:u16, target:u32".
		vars := map[string]int{}
		var widths []int
		var values []int
		if ops := strings.TrimSpace(cells[3]); ops != "" {
			for i, o := range strings.Split(ops, ",") {
				name, width, _ := strings.Cut(strings.TrimSpace(o), ":")
				w := map[string]int{"u16": 2, "u32": 4}[width]
				widths = append(widths, w)
				vars[name] = 3 + 4*i // some distinct operand values
				values = append(values, 3+4*i)
			}
		}
		if len(widths) != len(info.Operands) {
			t.Errorf("%s: documented with %d operands, has %d", info.Name, len(widths), len(info.Operands))
			continue
		}
		for i := range widths {
			if widths[i] != info.Operands[i] {
				t.Errorf("%s: operand %d documented as %d bytes, is %d", info.Name, i+1, widths[i], info.Operands[i])
			}
		}

		pops, pushes := StackUse(op, values)
		if got := evalDoc(t, cells[4], vars); got != pops {
			t.Errorf("%s: documented to pop %s (= %d), pops %d", info.Name, strings.TrimSpace(cells[4]), got, pops)
		}
		if got := evalDoc(t, cells[5], vars); got != pushes {
			t.Errorf("%s: documented to push %s (= %d), pushes %d", info.Name, strings.TrimSpace(cells[5]), got, pushes)
		}
	}
	for op := Op(0); op < numOps; op++ {
		if !seen[op] {
			t.Errorf("opcode %d (%s) is not in docs/vm.md", op, op)
		}
	}
}

// evalDoc evaluates a stack effect such as "2", "argc+1" or "2*n".
func evalDoc(t *testing.T, expr string, vars map[string]int) int {
	sum := 0
	for _, term := range strings.Split(strings.TrimSpace(expr), "+") {
		product := 1
		for _, f := range strings.Split(term, "*") {
			f = strings.TrimSpace(f)
			if n, err := strconv.Atoi(f); err == nil {
				product *= n
			} else if v, ok := vars[f]; ok {
				product *= v
			} else {
				t.Errorf("cannot evaluate %q in stack effect %q", f, expr)
			}
		}
		sum += product
	}
	return sum
}
