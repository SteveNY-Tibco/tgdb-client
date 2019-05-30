/**
 * Copyright 2019 TIBCO Software Inc.
 * All rights reserved.
 * 
 * Licensed under the Apache License, Version 2.0 (the "License"); You may not use this file except 
 * in compliance with the License.
 * A copy of the License is included in the distribution package with this file.
 * You also may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
 * 
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * <p/>
 * File name: TransactionImpl.java
 * Created on: 2/5/15
 * Created by: suresh 
 * <p/>
 * SVN Id: $Id: TransactionImpl.java 3158 2019-04-26 20:49:24Z kattaylo $
 */


package com.tibco.tgdb.connection.impl;

import com.tibco.tgdb.connection.TGTransaction;
import com.tibco.tgdb.exception.TGException;
import com.tibco.tgdb.pdu.TGInputStream;
import com.tibco.tgdb.pdu.TGOutputStream;

import java.io.IOException;

public class TransactionImpl implements TGTransaction {

    long transactionId;
    TransactionImpl(long transactionId) {
        this.transactionId = transactionId;
    }
    @Override
    public void writeExternal(TGOutputStream os) throws TGException, IOException {

    }

    @Override
    public void readExternal(TGInputStream is) throws TGException, IOException {

    }

    @Override
    public String toString() {
        return Long.toString(transactionId);
    }
}
