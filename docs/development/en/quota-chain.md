# Quota and rate-limit chain

Money is allocated through organization → team → person; key money limits are independent caps. RPM and TPM retain the full organization → team → person → key allocation chain. A project adds a parallel ceiling, not another allocation pool.

## Example

~~~text
numeric limit -> fixed allocation
null          -> share remaining parent capacity
0             -> disabled
~~~

## Contract

Increase limits parent-first; decrease child-first. Validate writes transactionally so a failed allocation cannot partially change roles or money. Resetting key spend does not reserve funds or erase historical personal/team/organization spend.

Routing inheritance is independent from quota inheritance. There is no request-budget reservation, so concurrent calls and long tasks can overshoot budgets.

## Source and verification

[internal/iam](../../../internal/iam)

~~~bash
bash scripts/regression.sh --verbose "TestRate|TestQuotaHierarchy|TestBudget|TestTeamless|TestPersonalKey"
~~~

[Test prerequisites](testing.md) · [Next: permissions](permissions.md)


Personal API key monetary caps do not reserve funds. Blank keys and console calls share actual remaining personal money. Key creation is allowed even when money is exhausted, but inference is rejected. RPM/TPM and organization/team/personal allocations retain their reservation rules.
