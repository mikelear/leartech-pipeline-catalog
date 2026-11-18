p = 'go/chartstd/main.go'
s = open(p).read()

s = s.replace('''	if wantStore == "" {''', '''	if listToggles != "" {
		decl, err := loadToggles(listToggles)
		if err != nil {
			fmt.Fprintln(os.Stderr, "chartstd:", err)
			os.Exit(2)
		}
		for _, t := range decl {
			off := t.Off
			if off == "" {
				off = "false"
			}
			fmt.Printf("%s|%s\\n", t.Path, off)
		}
		return
	}

	if wantStore == "" {''')

s = s.replace('''	flag.StringVar(&toggleDecl, "toggles", "",''', '''	flag.StringVar(&listToggles, "list-toggles", "",
		"print `<path>|<off>` per declared toggle and exit, so a shell step does not have "+
			"to parse the declaration itself")
	flag.StringVar(&toggleDecl, "toggles", "",''')

s = s.replace("\tvar toggleDecl string", "\tvar toggleDecl, listToggles string")

open(p, 'w').write(s)
print("patched")
