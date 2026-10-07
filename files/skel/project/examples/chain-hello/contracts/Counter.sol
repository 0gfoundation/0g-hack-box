// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

contract Counter {
    uint256 public number;
    event Incremented(address indexed by, uint256 value);

    function increment() external {
        number += 1;
        emit Incremented(msg.sender, number);
    }
}
