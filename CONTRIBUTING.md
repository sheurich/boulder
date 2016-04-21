Thanks for helping us build Boulder! This page contains requirements and guidelines for Boulder contributions.

# Patch Requirements
* All new functionality and fixed bugs must be accompanied by tests.
* Boulder currently implements the ACME-01 draft as defined by [acme-spec](https://tools.ietf.org/html/draft-ietf-acme-acme-01). If a spec change is required for Boulder functionality, you should propose it on the ACME mailing list (acme@ietf.org), possibly accompanid by a pull request on the [spec repo](https://github.com/ietf-wg-acme/acme/).
* All patches must meet the deployability requirements listed below.

# Review Requirements
* All boulder patches require approval from two reviewers.
  * We indicate review approval by attaching an r=username tag. After review, if changes need to be made, we add a needs-revision tag so we can easily skim the pull request list to see what needs review.
* Exceptions to the two-review rule:
  * Pull requests that change only documentation or test changes can be merged with one review, and shoudl be marked by either the submitter or the reviewer with the r1=test-only or r1=documentation labels, respectively. We define both terms strictly: any change to code that runs in production disqualifies this exception.
  * Pull requests from current master into the 'staging' branch can be merged without review. This is because any code in master has already been through the normal code review process. Similarly, pull requests from the current 'staging' branch into the 'release' branch can be merged without review. Pull requests into 'staging' or 'release' that aren't directly from master require the normal code review process. These pull requests should be marked by the submitter with the r0=branch-merge label.
* New commits pushed to a branch invalidate previous reviews. In other words, two reviewers must give positive reviews of a branch after its most recent pushed commit.
* You cannot review your own code.
* If a branch contains commits from multiple authors, it needs two reviewers who are not authors of commits on that branch.
* If a branch contains updates to files in the vendor/ directory, the author is responsible for running tests in all updated dependencies, and commenting in the review thread that they have done so. Reviewers must not approve reviews that have changes in vendor/ but lack a comment about tests.
* Review changes to or addition of tests just as rigorously as you review code changes. Consider: Do tests actually test what they mean to test? Is this the best way to test the functionality in question? Do the tests cover all the functionality in the patch, including error cases?
* Are there new RPCs or config fields? Make sure the patch meets the Deployability rules below.

# Patch Guidelines
* Please include helpful comments. No need to gratuitously comment clear code, but make sure it's clear why things are being done.
* Include information in your pull request about what you're trying to accomplish with your patch.
* Do not include `XXX`s or naked `TODO`s. Use the formats:
```
// TODO(<email-address>): Hoverboard + Time-machine unsupported until upstream patch.
// TODO(Issue #<num>): Pending hoverboard/time-machine interface.
```

# Squash merging

Once a pull requests has two reviews and the tests are passing, we'll merge it. We always use [squash merges](https://github.com/blog/2141-squash-your-commits) via GitHub's web interface. That means that during the course of your review you should generally not squash or amend commits, or force push. Even if the changes in each commit are small, keeping them separate makes it easier for us to review incremental changes to a pull request. Rest assured that those tiny changes will get squashed into a nice meaningful-size commit when we merge.

When submitting a squash merge, the merger should copy the URL of the pull
request into the body of the commit message.

If the Travis tests are failing on your branch, you should look at the logs to figure out why. Sometimes they fail spuriously, in which case you can post a comment requesting that a project owner kick the build.

# Deployability

We want to ensure that a new Boulder revision can be deployed to the currently running Boulder production instance without requiring config changes first. We also want to ensure that during a deploy, services can be restarted in any order. That means two things:

## Good zero values for config fields

Any newly added config field must have a usable [zero value](https://tour.golang.org/basics/12). That is to say, if a config field is absent, Boulder shouldn't crash or misbehave. If that config file names a file to be read, Boulder should be able to proceed without that file being read.

Note that there are some config fields that we want to be a hard requirement. To handle such a field, first add it as optional, then file an issue to make it required after the next deploy is complete.

In general, we would like our deploy process to be: deploy new code + old config; then immediately after deploy the same code + new config. This makes deploys cheaper so we can do them more often, and allows us to more readily separate deploy-triggered problems from config-triggered problems.

## Flag-gated RPCs

When you add a new RPC to a Boulder service (e.g. `SA.GetFoo()`), all components that call that RPC should wrap those calls in some flag. Generally this will be a boolean config field `UseFoo`. Since `UseFoo`'s zero value is false, a deploy with the existing config will not call `SA.GetFoo()`. Then, once the deploy is complete and we know that all SA instances support the `GetFoo()` RPC, we do a followup config deploy that sets `UseFoo()` to true.

# Dependencies

We vendorize all our dependencies using `godep`. Vendorizing means we copy the contents of those dependencies into our own repo. This has a few advantages:
  - If the remote sites that host our various dependencies are unreachable, it is still possible to build Boulder solely from the contents of its repo.
  - The versions of our dependencies can't change out from underneath us.

Note that this makes it possible to edit the local copy of our dependencies rather than the upstream copy. Occasionally we do this in great emergencies, but in general this is a bad idea because it means the next person to update that dependency will overwrite the changes.

Instead, it's better to contribute a patch upstream, then pull down changes. For dependencies that we expect to update semi-regularly, we create a fork in the letsencrypt organization, and vendorize that fork. For such forked dependencies, make changes by submitting a pull request to the letsencrypt fork. Once the pull request is reviewed and merged, (a) submit the changes as an upstream pull request, and (b) run `godep` to update to the latest version in the main Boulder. There are two advantages to this approach:
  - If upstream is slow to merge for any reason, we don't have to wait.
  - When we make changes, our first review is from other Boulder contributors rather than upstream. That way we make sure code meets our needs first before asking someone else to spend time on it.

When vendorizing dependencies, it's important to make sure tests pass on the version you are vendorizing. Currently we enforce this by requiring that pull requests containing a dependency update include a comment indicating that you ran the tests and that they succeeded, preferably with the command line you run them with.

## Problems or questions?

The best place to ask dev related questions is either [IRC](https://webchat.freenode.net/?channels=#letsencrypt-dev) or the [Community Forums](https://community.letsencrypt.org/)
