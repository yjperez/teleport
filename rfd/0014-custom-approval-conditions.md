---
authors: Forrest Marshall (forrest@gravitational.com)
state: discussion

---

## RFD 0014 - Custom Approval Conditions

Proposal for extending the Access Workflow API to support a more detailed
approval model, including custom scopes for approvers, and multi-party approval.

### Overview

#### Related Issues

- [#5007](https://github.com/gravitational/teleport/issues/5007) - Dual Authorization
- [#4309](https://github.com/gravitational/teleport/issues/4309) - Scoped Approvals
- [#4937](https://github.com/gravitational/teleport/issues/4937) - Workflow UI


#### Problem

The current approval model is very simplistic.  Any user which holds write permissions for
the `access_request` resource may update the state an any access request from `PENDING`
to `APPROVED` (or `DENIED`).  This is the only means by which a user may approve requests, so
any user that should be allowed to approve requests must currently be granted unilateral control
over all access requests.  This simplistic model works for cases where all access requests
are manged by a single entity (a 'plugin' user), but isn't granular enough to handle complex
controls.

#### Requirements

- Support configuration of access requests s.t. mutliple approvals from different
teleport users must be submitted prior to the access request transitioning from
a `PENDING` to an `APPROVED` state.

- Add custom permission scopes for approvers s.t. individual users may have permissions
to only approve certain access requests (e.g. members of team `dev` may be permitted to
approve access requests for `dev`, without necessarily being able to approve access requests
for `admin`).


### Proposition

Rather than simple write operations that are either always or never allowed, the new model
will be based on the idea of "proposals".  Users with the correct permissions will be able
to propose a state transition which does not come into effect until the propoals hit a
threshold specified by the `access_request` resource.

Say that `carol` has role `intern` and `alice` and `bob` both hold role `dev`.  We want
`carol` to be able to temporarily use role `staging` if both `alice` and `bob` approve of
her doing so.  We can construct a pair of roles that look something like this:

```yaml
kind: role
metadata:
  name: dev
spec:
  allow:
    # new configuration option permitting access request approval for
    # certain specific roles.
    approve:
      roles: ['staging']
    # ...
---
kind: role
metadata:
  name: intern
spec:
  allow:
    request:
      roles: ['staging']
      # new configuration option requiring a minimum of two approving
      # proposals before the request actually becomes approved.
      min_approvals: 2 
```

When `carol` generates her access request, the RBAC layer will automatically determine that
her request is subject to an approval threshold of `2` and encode this as part of the
pending request when it is stored in the backend.

When the first approval comes in, it will be stored, but the state of the access request will
not be updated since the request has not yet met its approval threshold.  Ex:

```
{
  "state": "PENDING",
  "approval_threshold": 2,
  "proposals": [
    {
      "state": "APPROVED",
      "user": "alice",
      "reason": "You seem trustworthy",
      ...
    }
  ],
  ...
}
```

When the second approval arrives, the auth server will automatically be able to evalute that the
approval threshold has been met, and transition the request from the `PENDING` to the `APPROVED`
state.


#### Supporting Extended Options

Ideally, all options available in the old approval style should be available in thresholded
approvals (i.e. an approval threshold of `1` should not be a special case).  Furthermore, in
a distributed system like teleport, order-independence is important.  This means that the
obvious solution of just treating the *final* approval (the one which actually triggers the
state transition) as a special case is out of the question.


##### Annotations

Calculating the `ResolveAnnotations` (annotations supplied upon approval/denial) of a request
is a bit tricky, but since they are of the form `map[string][]string` the annotations of all
proposals could theoretically be "summed" s.t. each key contains the values specified in all
proposals.  This isn't a perfect solution, as it would prevent users from treating the order
of annotations as meaningful, but there is no precedence for treating the order of annotations
as meaningful, so this isn't a massive concern.


##### Role Overrides

Role overrides are very tricky.  The existing system allows approvers to override the list of
roles to be granted by an access request (specifically, approvers can subselect, they cannot add
roles which were not present).  How this should map to thresholded approvls in incluser.  Say that
`dave` requests roles `foo`, `bar`, and `bin` with an approval threshold of `2`.  `bob` approves
`foo` and `bar`, and `alice` approves `bar` and `bin`.  The approval threshold has been reached,
but what roles should be granted? Technically, only `bar` reached the threshold.  It may seem
reasonable to only grant `bar`, since `bob`'s approval effectively denied `bin` and `alice`'s
approval effectively denied `foo`, but what if `carol` also submitted an approval for all 3?
Since the approval threshold is `2`, we are now racing to decide what the role subselection
will be, as one of the three approvals will not be present at the time when the final role
list is calculated, but any particular combination of approvals results in a different outcome.
We could treat this case as out of scope, after all approvals can already race within the single-approver model.
The single-approver model, however, was explicitly built for the purposes of control by automated
software or by a small team of well-coordinated admins.  Ideally, multi-party approval should
be more resilient to multiple possibly out of sync parties.

A partial solution to this conundrum is to tally proposals individually based on the state
they would resolve to (i.e. an approval for a specific set of roles counts only towards a final
request state with that exact set of roles).  Taking this strategy, the access request in the above
example would remain in a `PENDING` state because `alice`, `bob`, and `carol` effectively proposed
three separate possible outcomes.  Each possible outcome only has one supporting proposal, failing
to meet the threshold of `2`.  This doesn't elminate the possibility of nondeterminism due to ordering
(after all, 4 people voting for two possible states still results in a race), but it does ensure
that no `APPROVED` state is reached that wasn't exactly supported by the requisite number of
approvals (and does eliminate raciness and ambiguity so long as less than 2x threshold proposals
are received).
 

#### Partial Permission Overlap

Since users will now be able to approve access to some roles and not others, a new question
arises.  Can users approve a request for a subset they are allowed to approve access to, if
the original request contained roles that the user was not allowed to approve?  Say, for example,
that `alice` is allowed to approve `staging`, but not `prod`.  `bob` requests access to both `staging`
*and* `prod`.  Can `alice` approve the request but just for `staging`?  She obviously can't approve
it for `prod`, but she is allowed to approve for `staging`.  If so, can she *deny* the request?
It would be strange to have someone with no explicit permissions allowing them to control access
to `prod` be able to indirectly deny it.  On the other hand, if a given user is allowed to control
acces to a role, it seems equally strange to have them be powerless to deny access to the role
because of the presence of an unrelated role within the request (one that might not even make it
into the final approved state).

As discussed above, we *could* choose to treat proposals as relating only to the exact outcome
they describe.  In the above discussion, however, we were treating approvals as being strictly
related to the *exact* set of roles they proposed (i.e. `["foo"]` has no relation to `["foo","bar"]`).
If we take this route with denials, then denying access to `["staging"]` does not deny access
to `["staging","prod"]`.  That feels wrong too.  One possible resolution is to simply accept
inconsistency here and treat denials as being for all permutations which include the specified
roles, while approvals are for the exact permutation specified.

### Notes

- In the interest of simplicity, we won't bother supporting custom denial thresholds.  Instead,
any authorized approver may also deny, and 1 denial is always sufficient to deny the entire
request.

- An exception should be added to prevent users from approving their own requests.  Just a good
footgun to avoid.

### Future Work

- Look into supporting `where` clauses to give extra granularity to approval scopes, and possibly
for approval thresholds as well (through the latter may have limited usefulness since user traits
likely wouldn't be in-scope).
