#!/bin/bash
set -eo pipefail


NON_INTERACTIVE=false
DO_CLONE=false
while getopts "ca" arg; do
    case "$arg" in
        "a")
            NON_INTERACTIVE=true
            ;;
        "c")
            DO_CLONE=true
            ;;
        *)
            echo invalid option
            exit 1
            ;;
    esac
done

GHPAGESREPO="git@github.com:zrepl/zrepl.github.io.git"
SCRIPTDIR=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )
PUBLICDIR="${SCRIPTDIR}/public_git"

checkout_repo_msg() {
    echo "clone ${GHPAGESREPO} to ${PUBLICDIR}:"
}

exit_msg() {
    echo "error, exiting..."
}
trap exit_msg EXIT

if ! type sphinx-versioning >/dev/null; then
    echo "install sphinx-versioning and come back"
    exit 1
fi

cd "$SCRIPTDIR"

if [ ! -d "$PUBLICDIR" ]; then
    checkout_repo_msg
    if $DO_CLONE; then
        git clone "${GHPAGESREPO}" "${PUBLICDIR}"
    else
        exit 1
    fi
fi

if $NON_INTERACTIVE; then
    echo "non-interactive mode"
else
    echo -n "PRESS ENTER to confirm you commited and pushed docs changes and tags to the zrepl repo"
    read -r
fi

pushd "$PUBLICDIR" 

echo "verify we're in the GitHub pages repo..."
git remote get-url origin | grep -E "^${GHPAGESREPO}\$"
if [ "$?" -ne "0" ] ;then 
    checkout_repo_msg
    echo "finished checkout, please run again"
    exit 1
fi

echo "resetting GitHub pages repo to latest commit"
git fetch origin
git reset --hard origin/master

echo "cleaning GitHub pages repo"
git rm -rf .

popd

echo "building site"

flags="$(python3 gen-sphinx-versioning-flags.py)"
set -e
sphinx-versioning build \
   $flags \
   docs ./public_git \
   -- -c sphinxconf # older conf.py throw errors because they used
                    # version = subprocess.show_output(["git", "describe"])
                    # which fails when building with sphinxcontrib-versioning
set +e

CURRENT_COMMIT=$(git rev-parse HEAD)
git status --porcelain
if [[ "$(git status --porcelain)" != "" ]]; then
    CURRENT_COMMIT="${CURRENT_COMMIT}(dirty)" 
fi
COMMIT_MSG="sphinx-versioning render from publish.sh - $(date -u) - ${CURRENT_COMMIT}"

pushd "$PUBLICDIR"

echo "adding and commiting all changes in GitHub pages repo"
git add -A
git commit -m "$COMMIT_MSG"
git push origin master

