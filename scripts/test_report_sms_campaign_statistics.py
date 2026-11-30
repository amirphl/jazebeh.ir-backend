import unittest

import report_sms_campaign_statistics as report


class ReportSMSCampaignStatisticsTests(unittest.TestCase):
    def test_aggregate_matches_scheduler_shape(self):
        stats = report.aggregate(
            [
                {"total_parts": 1, "delivered_parts": 1, "undelivered_parts": 0, "unknown_parts": 0},
                {"total_parts": 1, "delivered_parts": 0, "undelivered_parts": 0, "unknown_parts": 1},
            ]
        )
        self.assertEqual(stats["aggregatedTotalRecords"], 2)
        self.assertEqual(stats["aggregatedTotalSent"], 1)
        self.assertEqual(stats["aggregatedTotalParts"], 2)
        self.assertEqual(stats["aggregatedTotalDeliveredParts"], 1)
        self.assertEqual(stats["aggregatedTotalUnKnownParts"], 1)


if __name__ == "__main__":
    unittest.main()
