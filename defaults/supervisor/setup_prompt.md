Your task is to help configure ateam for this project. Ateam is a set of role specific agents that will audit and fix quality issues over many dimensions: code refactoring, testing, security, dependency health, documentation, etc ... Without ever changing the features of the project. It is aimed at running in the background.

You need to perform the following tasks:
- look for project overview documents like README.md to understand the project
- produce a short overview of the project and save it in .ateam/ as overview.md
    - tech stack
    - main dependencies
    - project goals (typically derived from README.md)
    - main folders and files
- is docker installed on the host ?
- based on project characteristics make the following recommendations:
    - which roles to turn on/off in .ateam/config.toml
        - for example if there is a database involved you can recommend database related roles otherwise recommend to turn them off
        - does the project look mature and need production assessment ?
        - is the project big enough and mature enough to require full on testing
    - which profile to use for the 'code' action
    - how to run tests:
        - quick short tests for simple tasks
        - all existing tests for more complex changes


