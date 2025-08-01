package b

func ShadowWithAssignment() {
	x := 0
	{
		x := 1 // want "declaration of .x. shadows declaration at line 4"
		_ = x
		x = 2
	}
	_ = x
}

func ShadowWithCompoundAssignment() {
	x := 0
	{
		x := 1 // want "declaration of .x. shadows declaration at line 14"
		x = x + 1
	}
	_ = x
}

func ShadowWithPlusEquals() {
	x := 0
	{
		x := 1 // want "declaration of .x. shadows declaration at line 23"
		x += 1
	}
	_ = x
}

func ShadowWithIncrement() {
	x := 0
	{
		x := 1 // want "declaration of .x. shadows declaration at line 32"
		x++
	}
	_ = x
}

func ShadowWhenInnerUsed() {
	x := 0
	{
		x := 1 // OK - inner is used
		_ = x
	}
	_ = x
}

func ShadowWhenOuterNotUsedAfter() {
	x := 0
	_ = x
	{
		x := 1 // OK - outer is not used after inner
		_ = x
		x = 2
	}
}
