# ADR-0458: A PowerShell script runs unchanged in Atlas and at a prompt

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-15
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0047 hands a PowerShell script its instance variables as PowerShell variables: the
bootstrap sets one variable per instance variable and invokes the source as a script
block, with no arguments. A script written for PowerShell on its own does something
else. It declares its inputs in a `param()` block, logs with `Write-Host`, and often ends
with `exit 0`. Each of those failed in Atlas, and two of them failed silently.

- **A `param()` block shadowed the variables.** Its parameters are locals of the script
  block, and nothing bound them. A mandatory parameter failed as missing, because
  `-NonInteractive` cannot prompt. A parameter that was not mandatory ran on its default,
  or on an empty string, and the script exited 0 with a wrong value.
- **A namespace member is not a parameter name.** `[string]$application.CLIENT_ID` does
  not parse, and that parse error is what started this record. ADR-0456 put constants
  under `application.` and `server.`, so a script that wants one as a parameter needs
  another spelling.
- **Logs broke the result.** Run with `-Command` and stdout redirected, PowerShell writes
  `Write-Host`, `Write-Warning`, `Write-Verbose` and `Write-Information` to stdout. That
  was measured on 7.4.6, 7.5.3 and 7.6.2. stdout is where the bootstrap writes the result
  as JSON, so a script that logged failed with "decode output".
- **`exit` lost the output.** `exit` ends the interpreter, not only the script block.
  `"a"; exit 0` printed `a` through the default formatter and never reached the JSON
  conversion, so the result either failed to decode or was an accident of formatting.

The question: **how does one script file run unchanged both in Atlas and at a
PowerShell prompt, without breaking the scripts already deployed?**

## Decision drivers

- **One file, two hosts.** The script somebody tested at a prompt is the script they
  deploy, and the other way round.
- **No silent wrong values.** A declared input that Atlas does not supply has to fail
  visibly, as it does at a prompt.
- **PowerShell's semantics, not a copy of them.** Types, `Mandatory`, defaults and
  validation attributes act as PowerShell's own binder makes them act.
- **Deployed scripts keep working.** A script without a `param()` block sees what it saw,
  and every result keeps its shape.
- **Atlas does not parse script bodies at deploy** (ADR-0456). Anything that reads the
  script reads it at run time, with PowerShell's parser.

## Considered options

1. **Keep the contract and document it**: no `param()` block, no `Write-Host`, no `exit`.
2. **Bind the declared parameters from the variables of the same names**, and keep logs
   out of the result (chosen).
3. **Pass every variable as a parameter** (splat them all).
4. **Write the source to a `.ps1` file** and run it as a file with parameters.

## Decision outcome

Chosen option: **2**. It is the only option under which a script with a `param()` block
behaves as it does at a prompt without the bootstrap touching the disk.

### Binding

- The bootstrap creates the script block, reads the `param()` block from its syntax tree,
  and passes each declared parameter the instance variable of the same name.
- Names match without regard to case, as PowerShell matches parameters. Two variables
  that differ only in case resolve as they already did when the bootstrap set them as
  variables: the later one wins.
- Only declared names are passed. An advanced script (any `[Parameter()]` attribute or
  `[CmdletBinding()]`) refuses a parameter it does not declare, and common parameters
  such as `Verbose` are not in the block, so a variable of that name is never passed.
- A variable is passed as it is, `null` included, and PowerShell's binder decides.
  `Mandatory` refuses `null` and an empty string, and `[string]` makes `null` an empty
  string.
- A variable overrides a parameter's default. A parameter without a variable keeps its
  default. A mandatory parameter without a variable fails the run, and PowerShell names it.
- Every instance variable is still set as a variable, so a script without a block is
  unchanged. A script with a block may still read a variable it does not declare. That
  runs in Atlas and fails at a prompt, so it stays lenient on purpose: tightening it
  would break deployed scripts for no gain in Atlas.
- **Namespaces are declared as `[hashtable]$application`** (or `$server`). The namespace
  is one variable holding an object. `ConvertFrom-Json -AsHashtable` makes it an
  `OrderedHashtable`, which is a `Hashtable`. At a prompt, the same script takes
  `-application @{ TENANT_ID = '…' }`, and its body reads `$application.TENANT_ID` in
  both places.

### Output

- **Only the output stream is the result.** The bootstrap merges the warning, verbose,
  debug and information streams into the pipeline and writes them to stderr, with the
  prefixes PowerShell uses at a prompt (`WARNING:`, `VERBOSE:`, `DEBUG:`; `Write-Host`
  unprefixed). stderr is quoted when a run fails and discarded when it succeeds.
- **Each output object is collected as it was written**, and the result is assembled
  the way the plain assignment did it before. The shapes are pinned by a test against
  the real interpreter: one object, several, an array written as one object, `$null`,
  and nothing at all.
- **The result is written in a `finally` block.** `finally` runs when the script calls
  `exit`, so `exit 0` after output returns that output, and a non-zero exit keeps its
  code and fails the run.
- `$PSStyle.OutputRendering = 'PlainText'` from the preceding change still applies, and
  the worker strips terminal escapes from stderr for the PowerShell versions that ignore
  it.

### Consequences

- **Positive:** a script with a `param()` block runs unchanged in Atlas, in the
  Modeler's Test run (the same bootstrap), and at a prompt. `Mandatory`, types and
  validation attributes become the script's input check in Atlas. Logging no longer
  breaks a result, and a failure shows what the script logged before it failed.
- **Negative / trade-offs accepted:**
  - A script whose `param()` block names an instance variable now receives that
    variable's value instead of the parameter's default. That is the point of the
    change, but it changes behaviour for a deployed script that relied on the default
    winning, which before was always the case. It is called out in the changelog.
  - The logs of a successful run are discarded. A script has no log channel besides
    stderr, and stderr is read only on failure.
  - `Write-Information` without `-InformationAction Continue` is captured too, because
    redirecting the stream does not consult the preference. It appears in a failure's
    log although a prompt would not have shown it.
- **Differences to a prompt that remain:**
  - `#Requires` is ignored in a script block, so a missing module fails at the first
    call instead of up front.
  - `$PSScriptRoot` and `$MyInvocation.MyCommand.Path` are empty, so a script cannot
    dot-source a file beside it.
  - A missing mandatory parameter fails instead of prompting, and `Read-Host` fails
    under `-NonInteractive`.
- **Follow-ups / risks to watch:**
  - Python and JavaScript have no equivalent here. Their scripts read variables as
    globals, and an `argparse` or module entry point is a different question.
  - The Modeler could warn when a mandatory parameter has no variable of that name in
    the process. That needs a PowerShell parser in the browser, or an approximation.

## Pros and cons of the options

### Option 1 — keep the contract and document it
- Good: nothing changes, and one contract holds for all three languages.
- Bad: a script has to be edited to move between a prompt and Atlas, and edited back.
- Bad: documentation does not prevent the silent case. A `param()` block without
  `Mandatory` still runs on empty values with exit 0.

### Option 2 — bind the declared parameters (chosen)
- Good: PowerShell's own parser finds the parameters and PowerShell's own binder applies
  them, so types, validation and defaults are what the author tested.
- Good: only declared names are passed, so no variable reaches a script that did not ask.
- Bad: a value can now arrive two ways, as a variable and as a parameter. They hold the
  same value, but a reader of the bootstrap has to know both.
- Bad: the default-versus-variable change described above.

### Option 3 — pass every variable as a parameter
- Good: the simplest bootstrap.
- Bad: an advanced script refuses an undeclared parameter, so any instance holding one
  variable the script does not declare would fail. A simple script would collect them in
  `$args`. Neither is what the author wrote.

### Option 4 — write the source to a file and run it as one
- Good: the closest to a prompt. `#Requires`, `$PSScriptRoot` and `$MyInvocation` work.
- Bad: the author's source lands on disk on every run, as a file to clean up, a file the
  strict sandbox (ADR-0303) has to allow, and a file an operator may find. That departs
  from ADR-0047, which passes the source only through the environment.
- Bad: it solves neither binding nor logs nor `exit` by itself. Those still need
  everything option 2 does.
- It can be revisited if `#Requires` or `$PSScriptRoot` turn out to matter.

## Links

- extends ADR-0047 (polyglot script tasks): the variables-as-variables contract stays,
  and the parameter binding is added to it
- relates to ADR-0456 (constant namespaces): a namespace binds as a `[hashtable]`
  parameter
- relates to ADR-0303 (script sandbox): the bootstrap opens no file and no socket
