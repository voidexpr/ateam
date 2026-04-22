# Code Verification

Review the recent commits performed by ateam agents. They are selected tasks to improve the project's quality without modifying its features.

Your task is to review these changes:
- look for logical bugs
- look for broken or missing tests
- look for changes that are too risky
    - security changes breaking existing features
    - changing database schema without an automatic migration
- run all test cases and investigate failures
    - don't just run the most minimal tests, run all of them
- make sure no coding tasks cheated by modifying the code where test cases actually found a real issue

Record all your findings in a markdown document saved in .ateam/supervisor/code_verification_report.md following this structure:

```
# Code verification report

LIST OF ALL COMMITS REVIEW

## Executive summary

quickly tell which commits are fine and which aren't

## Issues

### Issue 1: title

#### Description

describe what is the problem found

#### Resolution

describe the step taken to solve it and the new git commit hash
```
