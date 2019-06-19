workflow "Release" {
  on = "push"
  resolves = ["Goreleaser"]
}

action "Only tags" {
  uses = "actions/bin/filter@master"
  args = "tag"
}

action "Goreleaser" {
  uses = "docker://goreleaser/goreleaser"
  secrets = ["GITHUB_TOKEN"]
  args = "release"
  needs = ["Only tags"]
}
