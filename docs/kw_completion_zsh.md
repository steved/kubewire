## kw completion zsh

Generate the autocompletion script for zsh

### Synopsis

Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(kw completion zsh)

To load completions for every new session, execute once:

#### Linux:

	kw completion zsh > "${fpath[1]}/_kw"

#### macOS:

	kw completion zsh > $(brew --prefix)/share/zsh/site-functions/_kw

You will need to start a new shell for this setup to take effect.


```
kw completion zsh [flags]
```

### Options

```
  -h, --help              help for zsh
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
  -d, --debug   Toggle debug logging
```

### SEE ALSO

* [kw completion](kw_completion.md)	 - Generate the autocompletion script for the specified shell

