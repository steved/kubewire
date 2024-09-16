## kw completion fish

Generate the autocompletion script for fish

### Synopsis

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	kw completion fish | source

To load completions for every new session, execute once:

	kw completion fish > ~/.config/fish/completions/kw.fish

You will need to start a new shell for this setup to take effect.


```
kw completion fish [flags]
```

### Options

```
  -h, --help              help for fish
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
  -d, --debug   Toggle debug logging
```

### SEE ALSO

* [kw completion](kw_completion.md)	 - Generate the autocompletion script for the specified shell

