"""Compare frozen, deterministically parsed decisions with the hidden oracle."""
import datetime as dt

CAUSES = {'taxonomy_migration', 'late_event_delivery', 'masked_multi_cause_regression',
          'traffic_quality', 'tracking_gap', 'mobile_regression', 'price_competition', 'release_regression'}
DECISIONS = {'alert', 'no_alert', 'insufficient_data'}


def assess(oracle, card):
    """Unknown inputs stay unscorable; accepted outcomes must come from the oracle."""
    if card.get('status') != 'completed' or not card.get('response', '').strip():
        return 'unscorable'
    decision = card.get('decision')
    cause = card.get('cause_type')
    if decision not in DECISIONS or card.get('contradictory') is not False:
        return 'unscorable'
    accepted = set(oracle.get('expected_decisions') or oracle.get('accepted_outcomes') or [])
    if oracle.get('cause_type') in ('none', 'clean'):
        accepted.add('no_alert')
    if decision in ('no_alert', 'insufficient_data'):
        if cause in CAUSES and cause != oracle.get('cause_type'):
            return 'false_alarm'
        if not card.get('verified_source'):
            return 'unscorable'
        if decision in accepted:
            return 'correct_' + decision
        return 'missed'
    if decision != 'alert':
        return 'unscorable'
    if accepted and accepted <= {'no_alert', 'insufficient_data'}:
        return 'false_alarm'
    expected_family = oracle.get('family')
    if expected_family and not card.get('family'):
        return 'unscorable'
    if expected_family and str(card['family']).casefold() != str(expected_family).casefold():
        return 'false_alarm'
    expected_week = oracle.get('week')
    if isinstance(expected_week, int):
        expected_week = (dt.date(2026, 1, 5) + dt.timedelta(weeks=expected_week)).isoformat()
    if expected_week and not card.get('week'):
        return 'unscorable'
    if expected_week and str(card['week']) != str(expected_week):
        return 'false_alarm'
    if not cause:
        return 'unscorable'
    if cause != oracle.get('cause_type'):
        return 'wrong_cause'
    if cause == 'masked_multi_cause_regression' and card.get('primary_cause') != 'checkout_regression':
        return 'unscorable' if not card.get('primary_cause') else 'wrong_cause'
    return 'found'
