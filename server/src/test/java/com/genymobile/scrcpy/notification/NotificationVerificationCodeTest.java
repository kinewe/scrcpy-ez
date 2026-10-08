package com.genymobile.scrcpy.notification;

import org.junit.Test;

import static org.junit.Assert.assertEquals;

public class NotificationVerificationCodeTest {
    @Test
    public void preservesCodeAndLeadingZerosFromSmsOnly() {
        for (String code : new String[] {"0048", "039571", "00123456"}) {
            assertEquals(code, NotificationVerificationCode.extract("com.android.mms", code));
            assertEquals("", NotificationVerificationCode.extract("com.synthetic.app", code));
            assertEquals("", NotificationVerificationCode.extract("com.miui.systemAdSolution", code));
        }
    }

    @Test
    public void rejectsUnboundedAndUnexpectedExtras() {
        for (Object value : new Object[] {null, 850329, "", "123", "123456789", " 850329", "850329\n", "８５０３２９", "AB1234",
                new Object()}) {
            assertEquals("", NotificationVerificationCode.extract("com.android.mms", value));
        }
    }
}
